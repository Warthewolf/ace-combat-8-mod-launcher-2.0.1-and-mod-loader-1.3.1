package main

// Regression tests from the final review of the game folder: a search still
// running when the player chooses the game folder, a storage folder on a drive
// that is not connected when the game moves, and choosing another copy while
// the game's own drive is not connected.

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// rgSlowLibrary turns Steam's list of libraries into a FIFO, so a search for
// the game waits on it the way it waits on a sleeping drive or an offline
// network share. The returned func lets the search go on.
func rgSlowLibrary(t *testing.T, steam string) (release func()) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a FIFO")
	}
	fifo := join(steam, "steamapps", "libraryfolders.vdf")
	if out, err := exec.Command("mkfifo", fifo).CombinedOutput(); err != nil {
		t.Skipf("can't make a FIFO: %v %s", err, out)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			// opening the writing end lets a reader that waits go on (it reads
			// nothing); without a reader yet, try again for a moment
			for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
				if w, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
					w.Close()
					break
				}
			}
			os.Remove(fifo) // later searches don't wait
		})
	}
}

func rgJSONPath(p string) string {
	b, _ := json.Marshal(map[string]string{"path": p})
	return string(b)
}

// core-search-overwrites-choice: the window's first state request is still
// searching (a slow Steam library) when the player chooses their copy of the
// game. The copy the search finds when it ends must not replace that choice.
func TestReviewSearchDoesNotOverwriteChoice(t *testing.T) {
	for _, how := range []string{"game", "pick-game"} {
		t.Run(how, func(t *testing.T) {
			y := newGame(t) // a copy in Steam's library: the search finds it
			steam := filepath.Dir(filepath.Dir(filepath.Dir(y.Root)))
			t.Setenv("ACE8_FAKE_STEAM", steam)
			release := rgSlowLibrary(t, steam)
			x := Game{Root: join(t.TempDir(), "Games", "ACE COMBAT 8")} // the player's copy
			write(t, x.GameExe(), "exe")
			settings.GameDir = ""
			lastNotFound = time.Time{}
			srv := httptest.NewServer(newMux(0))
			defer srv.Close()

			var first State
			finished := make(chan struct{})
			go func() { first = buildState(); close(finished) }() // it searches
			t.Cleanup(func() {
				release()
				select {
				case <-finished:
				case <-time.After(10 * time.Second):
				}
			})
			time.Sleep(300 * time.Millisecond)
			select {
			case <-finished:
				t.Fatal("the search did not wait for the slow library")
			default:
			}

			var out string
			if how == "game" {
				out = fixPost(t, srv, "game", rgJSONPath(x.Root))
			} else {
				t.Setenv("ACE8_FAKE_PICK", x.Root)
				out = fixPost(t, srv, "pick-game", `{}`)
			}
			if !strings.Contains(out, `"ok":true`) || !samePath(getSettings().GameDir, x.Root) {
				t.Fatalf("choosing the game folder: %s", out)
			}

			release() // the slow library answers now
			select {
			case <-finished:
			case <-time.After(10 * time.Second):
				t.Fatal("the search did not end")
			}
			if gd := getSettings().GameDir; !samePath(gd, x.Root) {
				t.Fatalf("the player chose %s; the search saved %s over it", x.Root, gd)
			}
			if !first.Game.Found || !samePath(first.Game.Root, x.Root) {
				t.Errorf("the search answered with %+v, not the copy the player chose", first.Game)
			}
			if g, ok := currentGame(); !ok || !samePath(g.Root, x.Root) {
				t.Errorf("game now %v %s", ok, g.Root)
			}
		})
	}
}

// core-use-copy-takes-unplugged-storage: the game X is on a drive that is not
// connected; its storage folder was chosen on the PC's own drive and holds X's
// switched-off UE4SS, Mod Loader and mods. The player uses another copy Y
// meanwhile ("Use the copy at …", or the folder dialog). Y must not take X's
// storage folder, and X has its mods back when its drive is.
func TestReviewUseCopyKeepsUnpluggedStorage(t *testing.T) {
	for _, how := range []string{"use the copy", "pick-game"} {
		t.Run(how, func(t *testing.T) {
			y := newGame(t)
			steam := filepath.Dir(filepath.Dir(filepath.Dir(y.Root)))
			t.Setenv("ACE8_FAKE_STEAM", steam)

			usb := t.TempDir()
			x := Game{Root: join(usb, "E", "SteamLibrary", "steamapps", "common", "ACE COMBAT 8")}
			write(t, x.GameExe(), "exe")
			write(t, x.Bootstrapper(), "eac")
			write(t, join(x.EACDir(), "Settings.json"), "{}")
			write(t, join(x.Paks(), "pakchunk0-Windows.utoc"), "game")
			installModding(t, x)
			container(t, join(x.loaderDir(), "Hornet"), "Hornet_P")
			container(t, join(x.Paks(), "~mods"), "SkinX_P")
			settings.GameDir = x.Root
			chosen := join(t.TempDir(), "My mod storage")
			if _, err := x.moveStorage(chosen); err != nil {
				t.Fatal(err)
			}
			if _, err := x.switchTo(modeUnmodded); err != nil {
				t.Fatal(err)
			}
			if !holdsMods(chosen) || !samePath(getSettings().StorageGame, x.Root) {
				t.Fatal("setup: X's mods are not switched off in its storage folder")
			}

			// the drive is not connected: the window offers the other copy
			hidden := usb + " (unplugged)"
			if err := os.Rename(join(usb, "E"), hidden); err != nil {
				t.Fatal(err)
			}
			lastNotFound = time.Time{}
			st := buildState()
			if st.Game.Found || !samePath(st.Game.Missing, x.Root) || !samePath(st.Game.Elsewhere, y.Root) {
				t.Fatalf("not the drive-not-connected case: %+v", st.Game)
			}
			srv := httptest.NewServer(newMux(0))
			defer srv.Close()
			var out string
			if how == "use the copy" {
				out = fixPost(t, srv, "game", rgJSONPath(st.Game.Elsewhere))
			} else {
				t.Setenv("ACE8_FAKE_PICK", y.Root)
				out = fixPost(t, srv, "pick-game", `{}`)
			}
			if !strings.Contains(out, `"ok":true`) || !samePath(getSettings().GameDir, y.Root) {
				t.Fatalf("choosing the other copy: %s", out)
			}
			gy := Game{Root: y.Root}
			if !samePath(gy.Storage(), gy.DefaultStorage()) {
				t.Errorf("the other copy %s took the storage folder %s of the game whose drive is not connected", y.Root, gy.Storage())
			}
			if m := gy.modeInfo().Mode; m == modeUnmodded {
				t.Errorf("the other copy shows X's switched-off mods (mode %s)", m)
			}

			// the drive is back, and the player chooses X again: all is there
			if err := os.Rename(hidden, join(usb, "E")); err != nil {
				t.Fatal(err)
			}
			if out := fixPost(t, srv, "game", rgJSONPath(x.Root)); !strings.Contains(out, `"ok":true`) {
				t.Fatal(out)
			}
			gx := Game{Root: x.Root}
			if !samePath(gx.Storage(), chosen) {
				t.Fatalf("X is back, but its storage folder is %s; its mods are in %s", gx.Storage(), chosen)
			}
			if m := gx.modeInfo().Mode; m != modeUnmodded {
				t.Fatalf("X: mode %s", m)
			}
			if out := fixPost(t, srv, "switch", `{"to":"modded"}`); !strings.Contains(out, `"ok":true`) {
				t.Fatalf("Modded: %s", out)
			}
			if mods, _ := scanPakMods(gx.loaderDir(), gx.tildeModsDir()); len(mods) != 2 {
				t.Errorf("X's mods after Modded: %v", keys(mods))
			}
			if !isFile(join(x.Win64(), "dwmapi.dll")) {
				t.Error("UE4SS is not back in X")
			}
		})
	}
}

// keepstorage-unplugged-chosen-folder: the storage folder was chosen on a
// drive that is not connected when the game moves to another library. The
// launcher must say the storage folder can't be found, not show an empty one,
// and the mods are back as soon as the drive is.
func TestReviewGameMovedWhileStorageUnplugged(t *testing.T) {
	for _, how := range []string{"found", "chosen"} {
		t.Run(how, func(t *testing.T) {
			g := newGame(t)
			installModding(t, g)
			container(t, join(g.Paks(), "~mods"), "SkinA_P")
			usb := join(t.TempDir(), "USB")
			chosen := join(usb, "AC8 storage")
			if _, err := g.moveStorage(chosen); err != nil {
				t.Fatal(err)
			}
			if _, err := g.switchTo(modeUnmodded); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(newMux(0))
			defer srv.Close()

			// the storage drive is not connected, and the game moves
			if err := os.Rename(usb, usb+" (unplugged)"); err != nil {
				t.Fatal(err)
			}
			var newRoot string
			if how == "found" {
				newRoot = steamMovesGame(t, g) // found again by itself
			} else {
				newRoot = join(t.TempDir(), "SteamLibrary", "steamapps", "common", "ACE COMBAT 8")
				os.MkdirAll(filepath.Dir(newRoot), 0o755)
				if err := os.Rename(g.Root, newRoot); err != nil {
					t.Fatal(err)
				}
				if out := fixPost(t, srv, "game", rgJSONPath(newRoot)); !strings.Contains(out, `"ok":true`) {
					t.Fatalf("choosing the game folder: %s", out)
				}
			}
			st := buildState()
			if !st.Game.Found || !samePath(st.Game.Root, newRoot) {
				t.Fatalf("game not found in its new place: %+v", st.Game)
			}
			if !st.Game.StorageMissing || !samePath(st.Game.Storage, chosen) {
				t.Fatalf("the chosen storage folder %s was dropped without a word: storage %s, missing=%v", chosen, st.Game.Storage, st.Game.StorageMissing)
			}
			if out := fixPost(t, srv, "switch", `{"to":"modded"}`); !strings.Contains(out, "can't be found") {
				t.Errorf("switched while the mods are out of reach: %s", out)
			}

			// the drive is back: the mods are there
			if err := os.Rename(usb+" (unplugged)", usb); err != nil {
				t.Fatal(err)
			}
			g2 := Game{Root: newRoot}
			if g2.storageMissing() || !samePath(g2.Storage(), chosen) {
				t.Fatalf("storage %s, missing=%v", g2.Storage(), g2.storageMissing())
			}
			if m := g2.modeInfo().Mode; m != modeUnmodded {
				t.Fatalf("mode %s", m)
			}
			if out := fixPost(t, srv, "switch", `{"to":"modded"}`); !strings.Contains(out, `"ok":true`) {
				t.Fatalf("Modded: %s", out)
			}
			if !isFile(join(g2.tildeModsDir(), "SkinA_P.utoc")) {
				t.Error("SkinA is not back in the game")
			}
		})
	}
}
