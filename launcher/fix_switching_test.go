package main

// Regression tests for the switch, the storage folder, and removing and
// restoring mods: crashes part way, folders on both sides, a game that moved.

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fixPost(t *testing.T, srv *httptest.Server, path, body string) string {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+"/api/"+path, strings.NewReader(body))
	req.Header.Set("X-Token", token)
	req.Host = "127.0.0.1:0"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return string(b)
}

// otherDrive makes every move between the game and storage a move to another
// drive, the way it is when the storage folder is on a USB drive.
func otherDrive(storage string) func(a, b string) error {
	return func(a, b string) error {
		if isInside(a, storage) != isInside(b, storage) {
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: syscall.EXDEV}
		}
		return os.Rename(a, b)
	}
}

func held(a, b string) error {
	return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("The process cannot access the file because it is being used by another process.")}
}

// leftovers lists what the launcher must never leave behind under root:
// half copies, its temporary folders, and "(older copy …)" folders.
func leftovers(root string) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && (strings.Contains(d.Name(), ".ace8-") || strings.Contains(d.Name(), "older copy") || strings.HasSuffix(d.Name(), ".partial")) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func names(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// park-1: a mod put into the game's ~mods by hand while Unmodded (as mod pages
// say) used to hide the whole switched-off collection as an "(older copy …)".
func TestFixHandAddedModWhileUnmoddedIsMerged(t *testing.T) {
	for _, button := range []string{modeUnmodded, modeModded} {
		t.Run(button, func(t *testing.T) {
			g := newGame(t)
			installModding(t, g)
			for _, n := range []string{"SkinA_P", "SkinB_P", "SkinC_P"} {
				container(t, join(g.Paks(), "~mods"), n)
			}
			if _, err := g.switchTo(modeUnmodded); err != nil {
				t.Fatal(err)
			}
			container(t, join(g.Paks(), "~mods"), "NewSkin_P")
			if m := g.modeInfo().Mode; m != modeMixed {
				t.Fatalf("mode %s", m)
			}
			res, err := g.switchTo(button)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Aside) != 0 {
				t.Errorf("set aside %v", res.Aside)
			}
			if button == modeUnmodded {
				if m := g.modeInfo().Mode; m != modeUnmodded {
					t.Fatalf("mode %s", m)
				}
				if _, err := g.switchTo(modeModded); err != nil {
					t.Fatal(err)
				}
			}
			mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
			want := []string{`~mods\NewSkin_P.utoc`, `~mods\SkinA_P.utoc`, `~mods\SkinB_P.utoc`, `~mods\SkinC_P.utoc`}
			if got := keys(mods); !reflect.DeepEqual(got, want) {
				t.Fatalf("mods in the game %q, want %q", got, want)
			}
			if l := leftovers(g.Storage()); len(l) != 0 {
				t.Errorf("hidden in storage: %v", l)
			}
		})
	}
}

// park-1: UE4SS and the Mod Loader copied into the game by hand while Unmodded
// used to hide every loader mod, script mod and the load order.
func TestFixHandCopiedUE4SSWhileUnmoddedIsMerged(t *testing.T) {
	for _, button := range []string{modeUnmodded, modeModded} {
		t.Run(button, func(t *testing.T) {
			g := newGame(t)
			discoveryTree(t, g)
			write(t, join(g.loaderDir(), "loadorder.txt"), "+SkyEdit\r\n")
			modsBefore, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
			if _, err := g.switchTo(modeUnmodded); err != nil {
				t.Fatal(err)
			}
			later := time.Now().Add(time.Hour)
			for p, s := range map[string]string{
				join(g.Win64(), "dwmapi.dll"):                                            "dwm-fresh",
				join(g.Win64(), "ue4ss", "UE4SS.dll"):                                    "ue4ss-fresh",
				join(g.Win64(), "ue4ss", "Mods", "IoStoreLoaderMod", "dlls", "main.dll"): "MZ-fresh..." + utf16le("1.1.0-ac8-1.1.2.0-ue5.4") + "...",
				join(g.Win64(), "ue4ss", "Mods", "IoStoreLoaderMod", "enabled.txt"):      "",
			} {
				write(t, p, s)
				os.Chtimes(p, later, later)
			}
			if _, err := g.switchTo(button); err != nil {
				t.Fatal(err)
			}
			if button == modeUnmodded {
				if _, err := g.switchTo(modeModded); err != nil {
					t.Fatal(err)
				}
			}
			mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
			if got, want := keys(mods), keys(modsBefore); !reflect.DeepEqual(got, want) {
				t.Fatalf("mods in the game %q, want %q", got, want)
			}
			if !isFile(join(g.loaderDir(), "loadorder.txt")) {
				t.Fatal("the load order is gone")
			}
			st := g.setupStatus()
			if !st.UE4SS || st.Loader == "" || !st.LoaderOn {
				t.Fatalf("setup %+v", st)
			}
			if b, _ := os.ReadFile(join(g.loaderDir(), "dlls", "main.dll")); !strings.HasPrefix(string(b), "MZ-fresh") {
				t.Fatalf("main.dll %q: the newer copy should stay", b)
			}
			if b, _ := os.ReadFile(join(g.ue4ssRoot(), "UE4SS.dll")); string(b) != "ue4ss-fresh" {
				t.Fatalf("UE4SS.dll %q: the newer copy should stay", b)
			}
			// the older UE4SS.dll is kept in the storage folder, not in the game
			kept := false
			for _, l := range leftovers(g.Storage()) {
				if strings.Contains(l, "ue4ss (older copy") && isFile(join(l, "UE4SS.dll")) {
					kept = true
				}
			}
			if !kept {
				t.Fatalf("the older UE4SS.dll was not kept: %v", leftovers(g.Storage()))
			}
			if l := leftovers(g.Root); len(l) != 0 {
				t.Fatalf("left in the game: %v", l)
			}
		})
	}
}

// park-1: the same mod on both sides: the newer copy stays, the whole older
// copy goes to the storage folder, and the two are never mixed.
func TestFixSameModOnBothSidesKeepsNewerWhole(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.Paks(), "~mods"), "SkinA_P")
	container(t, join(g.Paks(), "~mods"), "SkinB_P")
	old := time.Now().Add(-time.Hour)
	for _, ext := range []string{".utoc", ".ucas", ".pak"} {
		os.Chtimes(join(g.Paks(), "~mods", "SkinA_P"+ext), old, old)
	}
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	// a newer SkinA download, without its .pak, dropped into the game by hand
	write(t, join(g.Paks(), "~mods", "SkinA_P.utoc"), "toc-new")
	write(t, join(g.Paks(), "~mods", "SkinA_P.ucas"), "cas-new")
	res, err := g.switchTo(modeModded)
	if err != nil {
		t.Fatal(err)
	}
	tilde := join(g.Paks(), "~mods")
	if got := names(tilde); !reflect.DeepEqual(got, []string{"SkinA_P.ucas", "SkinA_P.utoc", "SkinB_P.pak", "SkinB_P.ucas", "SkinB_P.utoc"}) {
		t.Fatalf("~mods holds %v", got)
	}
	if b, _ := os.ReadFile(join(tilde, "SkinA_P.utoc")); string(b) != "toc-new" {
		t.Fatalf("SkinA_P.utoc %q", b)
	}
	if len(res.Aside) != 1 || !isInside(res.Aside[0], g.Storage()) {
		t.Fatalf("aside %v", res.Aside)
	}
	if got := names(res.Aside[0]); !reflect.DeepEqual(got, []string{"SkinA_P.pak", "SkinA_P.ucas", "SkinA_P.utoc"}) {
		t.Fatalf("older copy holds %v", got)
	}
	if l := leftovers(g.Paks()); len(l) != 0 {
		t.Fatalf("left in Paks, where the game would load it: %v", l)
	}
}

// park-2: the launcher is closed (or the PC goes off) while ~mods is being
// copied to a storage folder on another drive. Whichever button is pressed
// next, the player's mods come back whole.
func TestFixStoppedDuringCrossDriveSwitch(t *testing.T) {
	for _, button := range []string{modeModded, modeUnmodded} {
		t.Run(button, func(t *testing.T) {
			g := newGame(t)
			installModding(t, g)
			for _, n := range []string{"SkinA_P", "SkinB_P", "SkinC_P"} {
				container(t, join(g.Paks(), "~mods"), n)
			}
			before := treeSnapshot(t, g.Root)
			storage := g.Storage()
			gameTilde := join(g.Paks(), "~mods")
			parkTilde := join(g.OffDir(), "Game", "Content", "Paks", "~mods")
			t.Cleanup(func() { rename = os.Rename })
			rename = func(a, b string) error {
				copied := ""
				switch {
				case samePath(b, parkTilde) && !samePath(a, gameTilde):
					copied = a // the copy is about to get its real name
				case samePath(a, gameTilde) && !isInside(b, storage):
					copied = parkTilde // (launcher 2.0.4: the original is put aside after the copy)
				}
				if copied != "" {
					// only part of ~mods made it before the launcher was stopped
					for _, f := range []string{"SkinB_P.ucas", "SkinC_P.utoc", "SkinC_P.ucas", "SkinC_P.pak"} {
						os.Remove(join(copied, f))
					}
					write(t, join(copied, "SkinB_P.ucas.partial"), "ca")
					panic("the launcher was closed")
				}
				return otherDrive(storage)(a, b)
			}
			func() {
				defer func() { recover() }()
				g.switchTo(modeUnmodded)
			}()
			rename = otherDrive(storage) // the launcher starts again
			if s := buildState(); s.Mode.Mode != modeMixed || s.UnfinishedSw != modeUnmodded {
				t.Fatalf("after the crash: mode %q, unfinished %q", s.Mode.Mode, s.UnfinishedSw)
			}
			if _, err := g.switchTo(button); err != nil {
				t.Fatal(err)
			}
			if button == modeUnmodded {
				if m := g.modeInfo().Mode; m != modeUnmodded || hasFiles(gameTilde) {
					t.Fatalf("mode %s", m)
				}
				if _, err := g.switchTo(modeModded); err != nil {
					t.Fatal(err)
				}
			}
			if after := treeSnapshot(t, g.Root); !reflect.DeepEqual(before, after) {
				t.Fatalf("the game is not as it was:\n%v\n%v", before, after)
			}
			if s := buildState(); s.UnfinishedSw != "" || s.Mode.Mode != modeModded {
				t.Fatalf("mode %q, unfinished %q", s.Mode.Mode, s.UnfinishedSw)
			}
			if l := leftovers(storage); len(l) != 0 {
				t.Fatalf("left in storage: %v", l)
			}
		})
	}
}

// park-2: stopped when a copy to another drive is at its real place but the
// original is still there too. The original is what counts; the copy is kept
// aside in the storage folder and never mixed in.
func TestFixStoppedWithCopyAndOriginalBothThere(t *testing.T) {
	for _, button := range []string{modeModded, modeUnmodded} {
		t.Run(button, func(t *testing.T) {
			g := newGame(t)
			installModding(t, g)
			for _, n := range []string{"SkinA_P", "SkinB_P"} {
				container(t, join(g.Paks(), "~mods"), n)
			}
			before := treeSnapshot(t, g.Root)
			storage := g.Storage()
			gameTilde := join(g.Paks(), "~mods")
			parkTilde := join(g.OffDir(), "Game", "Content", "Paks", "~mods")
			t.Cleanup(func() { rename = os.Rename })
			rename = func(a, b string) error {
				if samePath(a, gameTilde) && samePath(b, parkTilde) {
					// a copy at the real place, with newer dates, and the original untouched
					write(t, join(parkTilde, "SkinB_P.pak"), "pak")
					write(t, join(parkTilde, "SkinB_P.ucas.partial"), "ca")
					panic("the launcher was closed")
				}
				return otherDrive(storage)(a, b)
			}
			func() {
				defer func() { recover() }()
				g.switchTo(modeUnmodded)
			}()
			rename = otherDrive(storage)
			if _, err := g.switchTo(button); err != nil {
				t.Fatal(err)
			}
			if button == modeUnmodded {
				if _, err := g.switchTo(modeModded); err != nil {
					t.Fatal(err)
				}
			}
			if after := treeSnapshot(t, g.Root); !reflect.DeepEqual(before, after) {
				t.Fatalf("the game is not as it was:\n%v\n%v", before, after)
			}
			kept := false
			for _, l := range leftovers(storage) {
				kept = kept || isFile(join(l, "SkinB_P.ucas.partial"))
			}
			if !kept {
				t.Fatalf("the copy was not kept aside: %v", leftovers(storage))
			}
		})
	}
}

// park-3: a switch fails, and putting it back fails too. Pressing the switch
// again, as the message says, used to split ue4ss: half in the game, half
// hidden as an "(older copy …)".
func TestFixRetryAfterUndoFailed(t *testing.T) {
	for _, again := range []string{modeUnmodded, modeModded} {
		t.Run(again, func(t *testing.T) {
			g := newGame(t)
			discoveryTree(t, g)
			before := treeSnapshot(t, g.Root)
			gameUE := join(g.Win64(), "ue4ss")
			lockedDll := join(gameUE, "UE4SS.dll")
			parkedLoader := join(g.OffDir(), "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "dlls", "main.dll")
			t.Cleanup(func() { rename = os.Rename })
			rename = func(a, b string) error {
				switch {
				case samePath(a, gameUE), samePath(a, lockedDll): // a program watches ue4ss and has UE4SS.dll open
					return held(a, b)
				case isInside(parkedLoader, a): // a virus scanner reads the switched-off Mod Loader
					return held(a, b)
				}
				return os.Rename(a, b)
			}
			_, err := g.switchTo(modeUnmodded)
			if err == nil || !strings.Contains(err.Error(), "could not put everything back") {
				t.Fatalf("expected a failed undo, got %v", err)
			}
			if s := buildState(); s.UnfinishedSw != modeUnmodded || s.Mode.Mode != modeMixed {
				t.Fatalf("mode %q, unfinished %q", s.Mode.Mode, s.UnfinishedSw)
			}
			rename = os.Rename // the files are free now
			if _, err := g.switchTo(again); err != nil {
				t.Fatal(err)
			}
			if again == modeUnmodded {
				if m := g.modeInfo().Mode; m != modeUnmodded {
					t.Fatalf("mode %s", m)
				}
				if _, err := g.switchTo(modeModded); err != nil {
					t.Fatal(err)
				}
			}
			if after := treeSnapshot(t, g.Root); !reflect.DeepEqual(before, after) {
				t.Fatalf("the game is not as it was:\n%v\n%v", before, after)
			}
			if st := g.setupStatus(); !st.UE4SS || st.Loader == "" {
				t.Fatalf("setup %+v", st)
			}
			if s := buildState(); s.UnfinishedSw != "" || s.Mode.Mode != modeModded {
				t.Fatalf("mode %q, unfinished %q", s.Mode.Mode, s.UnfinishedSw)
			}
			if l := leftovers(g.Storage()); len(l) != 0 {
				t.Fatalf("hidden in storage: %v", l)
			}
		})
	}
}

// park-11: after a copy to another drive, the original ~mods is put out of the
// way outside Paks, where the game would not load what is left of it, and
// leftovers are cleared at the next switch.
func TestFixCrossDriveOriginalNotLeftInPaks(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.Paks(), "~mods"), "SkinA_P")
	storage := g.Storage()
	var putAside string
	t.Cleanup(func() { rename = os.Rename })
	rename = func(a, b string) error {
		if samePath(a, join(g.Paks(), "~mods")) && !isInside(b, storage) {
			putAside = b
		}
		return otherDrive(storage)(a, b)
	}
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	if putAside == "" || isInside(putAside, g.Paks()) {
		t.Fatalf("the original ~mods was put aside at %q, inside Paks", putAside)
	}
	// what a launcher stopped while deleting it would leave: not counted, and cleared away
	left := join(g.Root, "Game", "Content", deletingDir, "~mods")
	container(t, left, "SkinA_P")
	if m := g.modeInfo().Mode; m != modeUnmodded {
		t.Fatalf("mode %s", m)
	}
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	if l := leftovers(g.Root); len(l) != 0 {
		t.Fatalf("left in the game: %v", l)
	}
	if mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir()); len(mods) != 1 {
		t.Fatalf("mods %v", keys(mods))
	}
}

// park-5: "Open folder" on a chosen storage folder that can't be found must not
// make a new empty one there; with no game found, it must not make
// ". - mod storage" in the launcher's working folder.
func TestFixRevealDoesNotMakeStorageFolders(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()
	// the default folder is made the first time it is opened
	if out := fixPost(t, srv, "reveal", `{"what":"storage"}`); !strings.Contains(out, `"ok":true`) || !isDir(g.DefaultStorage()) {
		t.Fatalf("default: %s", out)
	}
	parent := t.TempDir()
	chosen := join(parent, "AC8 storage")
	os.MkdirAll(chosen, 0o755)
	if _, err := g.moveStorage(chosen); err != nil {
		t.Fatal(err)
	}
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	os.Rename(chosen, join(parent, "AC8 storage (moved)"))
	out := fixPost(t, srv, "reveal", `{"what":"storage"}`)
	if !strings.Contains(out, "can't be found") || lexists(chosen) || !g.storageMissing() {
		t.Fatalf("reveal of a missing storage folder: %s; made again: %v", out, lexists(chosen))
	}
	// no game found yet
	t.Chdir(t.TempDir())
	settings = Settings{}
	lastNotFound = time.Time{}
	out = fixPost(t, srv, "reveal", `{"what":"storage"}`)
	if strings.Contains(out, `"ok":true`) {
		t.Fatalf("no game: %s", out)
	}
	if ents, _ := os.ReadDir("."); len(ents) != 0 {
		t.Fatalf("made in the working folder: %v", names("."))
	}
}

// steamMovesGame does what Steam's "Move install folder" does: the game folder
// goes to another library, the storage folder next to it stays.
func steamMovesGame(t *testing.T, g Game) string {
	steam, newLib := t.TempDir(), t.TempDir()
	write(t, join(steam, "steam.exe"), "")
	write(t, join(steam, "steamapps", "libraryfolders.vdf"), `"libraryfolders" { "0" { "path" "`+strings.ReplaceAll(steam, `\`, `\\`)+`" } "1" { "path" "`+strings.ReplaceAll(newLib, `\`, `\\`)+`" } }`)
	write(t, join(newLib, "steamapps", "appmanifest_2288340.acf"), `"AppState" { "appid" "2288340" "installdir" "ACE COMBAT 8" }`)
	newRoot := join(newLib, "steamapps", "common", "ACE COMBAT 8")
	os.MkdirAll(filepath.Dir(newRoot), 0o755)
	if err := os.Rename(g.Root, newRoot); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACE8_FAKE_STEAM", steam)
	lastNotFound = time.Time{}
	return newRoot
}

// park-4 and inst-11: the game folder moves (Steam's "Move install folder", or
// choosing it again in its new place). The switched-off and removed mods stay
// in use, and restored mods go into the game in its new place.
func TestFixGameMovedKeepsStorage(t *testing.T) {
	for _, tc := range []struct {
		name          string
		chosen, byAPI bool
	}{{"default found", false, false}, {"chosen found", true, false}, {"default picked", false, true}, {"chosen picked", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			g := newGame(t)
			installModding(t, g)
			container(t, join(g.Paks(), "~mods"), "SkinA_P")
			container(t, join(g.loaderDir(), "Hornet"), "Hornet_P")
			container(t, join(g.loaderDir(), "Legacy"), "Legacy_P")
			if tc.chosen {
				if _, err := g.moveStorage(join(t.TempDir(), "AC8 storage")); err != nil {
					t.Fatal(err)
				}
			}
			for _, n := range []string{"Hornet", "Legacy"} {
				if _, err := g.removeToStorage("pak", n, join(g.loaderDir(), n), false); err != nil {
					t.Fatal(err)
				}
			}
			// a mod removed with an older launcher remembers only its full path
			for _, r := range g.listRemoved() {
				if dir, ri, err := g.removedEntry(r.ID); err == nil && ri.Name == "Legacy" {
					ri.Rel = ""
					writeRemovedInfo(dir, ri)
				}
			}
			if _, err := g.switchTo(modeUnmodded); err != nil {
				t.Fatal(err)
			}
			oldRoot, oldStorage := g.Root, g.Storage()
			var g2 Game
			if tc.byAPI {
				newRoot := join(t.TempDir(), "SteamLibrary", "steamapps", "common", "ACE COMBAT 8")
				os.MkdirAll(filepath.Dir(newRoot), 0o755)
				if err := os.Rename(g.Root, newRoot); err != nil {
					t.Fatal(err)
				}
				srv := httptest.NewServer(newMux(0))
				defer srv.Close()
				if out := fixPost(t, srv, "game", `{"path":"`+strings.ReplaceAll(newRoot, `\`, `\\`)+`"}`); !strings.Contains(out, `"ok":true`) {
					t.Fatalf("choosing the game folder: %s", out)
				}
				g2 = Game{Root: newRoot}
			} else {
				newRoot := steamMovesGame(t, g)
				var ok bool
				if g2, ok = currentGame(); !ok || !samePath(g2.Root, newRoot) {
					t.Fatalf("game not found again: %v %s", ok, g2.Root)
				}
			}
			if !samePath(g2.Storage(), oldStorage) {
				t.Fatalf("storage is now %s, the mods are in %s", g2.Storage(), oldStorage)
			}
			if m := g2.modeInfo().Mode; m != modeUnmodded {
				t.Fatalf("mode %s", m)
			}
			if _, err := g2.switchTo(modeModded); err != nil {
				t.Fatal(err)
			}
			rm := g2.listRemoved()
			if len(rm) != 2 {
				t.Fatalf("removed %+v", rm)
			}
			for _, r := range rm {
				if err := g2.restoreRemoved(r.ID); err != nil {
					t.Fatal(err)
				}
			}
			for _, n := range []string{"Hornet", "Legacy"} {
				if !isFile(join(g2.loaderDir(), n, n+"_P.utoc")) {
					t.Errorf("%s is not back in the game", n)
				}
			}
			if lexists(oldRoot) {
				t.Errorf("the old game folder was made again: %v", treeSnapshot(t, oldRoot))
			}
			if mods, _ := scanPakMods(g2.loaderDir(), g2.tildeModsDir()); len(mods) != 3 {
				t.Errorf("mods %v", keys(mods))
			}
		})
	}
}

// inst-11: a removed mod whose saved place can't be matched to this game is
// not restored to some other folder.
func TestFixRestoreRefusesUnknownPlace(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.loaderDir(), "Hornet"), "Hornet_P")
	dir, err := g.removeToStorage("pak", "Hornet", join(g.loaderDir(), "Hornet"), false)
	if err != nil {
		t.Fatal(err)
	}
	_, ri, _ := g.removedEntry(filepath.Base(dir))
	elsewhere := join(t.TempDir(), "Somewhere", "Hornet")
	ri.From, ri.Rel = elsewhere, ""
	writeRemovedInfo(dir, ri)
	if err := g.restoreRemoved(filepath.Base(dir)); err == nil || !strings.Contains(err.Error(), "can't tell where") {
		t.Fatalf("err %v", err)
	}
	if lexists(filepath.Dir(elsewhere)) || !isDir(join(dir, "Hornet")) {
		t.Fatal("moved anyway")
	}
}

// park-6: "Use the default" while the chosen storage folder's drive is not
// connected used to say "Nothing needed moving" and forget the folder.
func TestFixStorageMoveFromMissingFolder(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	chosen := join(t.TempDir(), "USB", "AC8 storage")
	os.MkdirAll(chosen, 0o755)
	if _, err := g.moveStorage(chosen); err != nil {
		t.Fatal(err)
	}
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	usb := filepath.Dir(chosen)
	if err := os.Rename(usb, usb+" (unplugged)"); err != nil {
		t.Fatal(err)
	}
	_, err := g.moveStorage("")
	if err == nil || !strings.Contains(err.Error(), chosen) || !strings.Contains(err.Error(), "Connect the drive") {
		t.Fatalf("err %v", err)
	}
	if !samePath(g.Storage(), chosen) || !g.storageMissing() {
		t.Fatalf("storage changed to %s", g.Storage())
	}
	// a double click does not count as reading the message
	if _, err := g.moveStorage(""); err == nil {
		t.Fatal("went on at once")
	}
	// the drive comes back under another letter: picking the folder there just uses it
	back := join(t.TempDir(), "E", "AC8 storage")
	os.MkdirAll(filepath.Dir(back), 0o755)
	if err := os.Rename(join(usb+" (unplugged)", "AC8 storage"), back); err != nil {
		t.Fatal(err)
	}
	if _, err := g.moveStorage(g.storageTarget(back)); err != nil {
		t.Fatal(err)
	}
	if !samePath(g.Storage(), back) || g.modeInfo().Mode != modeUnmodded {
		t.Fatalf("storage %s, mode %s", g.Storage(), g.modeInfo().Mode)
	}
	// gone for good: pressing again after reading the message goes on without it
	if err := os.RemoveAll(filepath.Dir(back)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.moveStorage(""); err == nil {
		t.Fatal("not told")
	}
	leavingMissing.at = time.Now().Add(-5 * time.Second)
	if _, err := g.moveStorage(""); err != nil {
		t.Fatal(err)
	}
	if !samePath(g.Storage(), g.DefaultStorage()) || g.customStorage() {
		t.Fatalf("storage %s", g.Storage())
	}
}

// park-8: the launcher is closed while the storage folder is being copied to
// another drive. The removed mods stay in view, and choosing the folder again
// finishes the move instead of being refused.
func TestFixStorageMoveStoppedPartWay(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.Paks(), "~mods"), "SkinA_P")
	container(t, join(g.Paks(), "~mods"), "SkinB_P")
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	if _, err := g.removeToStorage("tilde", mods[0].Key, mods[0].Path, mods[0].IsFile); err != nil {
		t.Fatal(err)
	}
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	old := g.Storage()
	target := join(t.TempDir(), "USB drive", "AC8 storage")
	os.MkdirAll(target, 0o755)
	oldOff, newOff := join(old, "Switched off"), join(target, "Switched off")
	toUSB := func(a, b string) error {
		if isInside(b, target) && !isInside(a, target) {
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: syscall.EXDEV}
		}
		return os.Rename(a, b)
	}
	t.Cleanup(func() { rename = os.Rename })
	rename = func(a, b string) error {
		if (samePath(b, newOff) && !samePath(a, oldOff)) || (samePath(a, oldOff) && !isInside(b, target)) {
			panic("the launcher was closed") // Switched off is copied, but not finished
		}
		return toUSB(a, b)
	}
	func() {
		defer func() { recover() }()
		g.moveStorage(target)
	}()
	rename = toUSB
	if !samePath(g.Storage(), old) || len(g.listRemoved()) != 1 || g.modeInfo().Mode != modeUnmodded {
		t.Fatalf("after the crash: storage %s, removed %d, mode %s", g.Storage(), len(g.listRemoved()), g.modeInfo().Mode)
	}
	if _, err := g.moveStorage(target); err != nil {
		t.Fatalf("trying the same folder again: %v", err)
	}
	if !samePath(g.Storage(), target) || len(g.listRemoved()) != 1 || g.modeInfo().Mode != modeUnmodded {
		t.Fatalf("after the move: storage %s, removed %d, mode %s", g.Storage(), len(g.listRemoved()), g.modeInfo().Mode)
	}
	if l := leftovers(target); len(l) != 0 || lexists(join(target, movingHere)) {
		t.Fatalf("left over: %v", l)
	}
	if hasFiles(old) {
		t.Fatalf("still in the old folder: %v", treeSnapshot(t, old))
	}
	if err := g.restoreRemoved(g.listRemoved()[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	if mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir()); len(mods) != 2 {
		t.Fatalf("mods %v", keys(mods))
	}
}

// park-9: the settings can't be saved at the end of a storage move: the old
// folder stays in use, with everything in it, and removed mods still go back
// to the right place.
func TestFixStorageMoveSettingsNotSaved(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.Paks(), "~mods"), "SkinA_P")
	container(t, join(g.Paks(), "~mods"), "SkinB_P")
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	if _, err := g.removeToStorage("tilde", mods[0].Key, mods[0].Path, mods[0].IsFile); err != nil {
		t.Fatal(err)
	}
	old := g.Storage()
	target := join(t.TempDir(), "AC8 storage")
	os.MkdirAll(target, 0o755)
	write(t, join(settingsPath()+".tmp", "x"), "") // settings.json can't be written
	_, err := g.moveStorage(target)
	if err == nil || !strings.Contains(err.Error(), "could not save its settings") || !strings.Contains(err.Error(), "not changed") {
		t.Fatalf("err %v", err)
	}
	os.RemoveAll(settingsPath() + ".tmp")
	if !samePath(g.Storage(), old) || g.modeInfo().Mode != modeUnmodded || len(g.listRemoved()) != 1 {
		t.Fatalf("storage %s, mode %s, removed %d", g.Storage(), g.modeInfo().Mode, len(g.listRemoved()))
	}
	_, ri, _ := g.removedEntry(g.listRemoved()[0].ID)
	if isInside(ri.From, target) {
		t.Fatalf("removed.json points into the folder not in use: %s", ri.From)
	}
	if hasFiles(target) {
		t.Fatalf("left in the new folder: %v", treeSnapshot(t, target))
	}
	if err := g.restoreRemoved(g.listRemoved()[0].ID); err != nil {
		t.Fatal(err)
	}
	if mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir()); len(mods) != 2 || !isInside(g.tildeModsDir(), old) {
		t.Fatalf("mods %v in %s", keys(mods), g.tildeModsDir())
	}
}

// park-7 and inst-10: restoring the only ~mods mod goes to the side the mode
// says, so the game is never left "Half switched".
func TestFixRestoreFollowsTheMode(t *testing.T) {
	for _, removedWhile := range []string{modeModded, modeUnmodded} {
		t.Run("removed while "+removedWhile, func(t *testing.T) {
			g := newGame(t)
			installModding(t, g)
			container(t, join(g.Paks(), "~mods"), "OnlySkin_P")
			restoreWhile := modeUnmodded
			if removedWhile == modeUnmodded {
				restoreWhile = modeModded
				if _, err := g.switchTo(modeUnmodded); err != nil {
					t.Fatal(err)
				}
			}
			mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
			if _, err := g.removeToStorage("tilde", mods[0].Key, mods[0].Path, mods[0].IsFile); err != nil {
				t.Fatal(err)
			}
			if _, err := g.switchTo(restoreWhile); err != nil {
				t.Fatal(err)
			}
			if err := g.restoreRemoved(g.listRemoved()[0].ID); err != nil {
				t.Fatal(err)
			}
			mods, _ = scanPakMods(g.loaderDir(), g.tildeModsDir())
			if m := g.modeInfo().Mode; m != restoreWhile || len(mods) != 1 {
				t.Fatalf("mode %s, mods %v", m, keys(mods))
			}
		})
	}
}

// inst-10: while a switch is unfinished, a restore is refused rather than guessed.
func TestFixRestoreRefusedWhileHalfSwitched(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.loaderDir(), "Hornet"), "Hornet_P")
	if _, err := g.removeToStorage("pak", "Hornet", join(g.loaderDir(), "Hornet"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	container(t, join(g.Paks(), "~mods"), "NewSkin_P")
	if err := g.restoreRemoved(g.listRemoved()[0].ID); err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("err %v", err)
	}
	if len(g.listRemoved()) != 1 {
		t.Fatal("restored anyway")
	}
}

// inst-18: a loose mod whose restore fails part way is put back whole, and can
// be restored once the file is free.
func TestFixLooseRestoreAllOrNothing(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.Paks(), "~mods"), "Loose_P")
	container(t, join(g.Paks(), "~mods"), "Other_P")
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	var m *PakMod
	for _, x := range mods {
		if x.Key == `~mods\Loose_P.utoc` {
			m = x
		}
	}
	if m == nil {
		t.Fatalf("mods %v", keys(mods))
	}
	if _, err := g.removeToStorage("tilde", m.Key, m.Path, m.IsFile); err != nil {
		t.Fatal(err)
	}
	id := g.listRemoved()[0].ID
	t.Cleanup(func() { rename = os.Rename })
	rename = func(a, b string) error {
		if strings.HasSuffix(b, "Loose_P.ucas") && isInside(b, g.Paks()) {
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("There is not enough space on the disk.")}
		}
		return os.Rename(a, b)
	}
	err := g.restoreRemoved(id)
	if err == nil || !strings.Contains(err.Error(), "Nothing was changed") {
		t.Fatalf("err %v", err)
	}
	if got := names(join(g.Paks(), "~mods")); !reflect.DeepEqual(got, []string{"Other_P.pak", "Other_P.ucas", "Other_P.utoc"}) {
		t.Fatalf("half restored: %v", got)
	}
	rename = os.Rename
	if err := g.restoreRemoved(id); err != nil {
		t.Fatal(err)
	}
	if mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir()); len(mods) != 2 || len(g.listRemoved()) != 0 {
		t.Fatalf("mods %v, removed %v", keys(mods), g.listRemoved())
	}
}

// inst-16: copying over a file (a Mod Loader update) never leaves no file:
// the new one replaces the old one in one step, and a held file is waited for.
func TestFixCopyFileNeverLeavesNoFile(t *testing.T) {
	newGame(t)
	dir := t.TempDir()
	src, dst := join(dir, "new", "main.dll"), join(dir, "dlls", "main.dll")
	write(t, src, "new")
	write(t, dst, "old")
	fails := 2
	t.Cleanup(func() { rename = os.Rename })
	rename = func(a, b string) error {
		if strings.HasSuffix(a, ".partial") && fails != 0 {
			fails--
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("Access is denied.")}
		}
		return os.Rename(a, b)
	}
	if err := copyFile(src, dst, 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Fatalf("dst %q", b)
	}
	// held for good: the old file stays, and no .partial is left
	write(t, src, "newer")
	fails = -1
	if err := copyFile(src, dst, 0o644); err == nil {
		t.Fatal("no error")
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Fatalf("dst %q", b)
	}
	if got := names(filepath.Dir(dst)); !reflect.DeepEqual(got, []string{"main.dll"}) {
		t.Fatalf("left %v", got)
	}
}

// park-10: on Linux a "~Mods" folder made by hand is another folder than
// "~mods", but the game loads it all the same, so it is switched off too.
func TestFixOtherCapitalsAreSwitched(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.Paks(), "~Mods"), "CapsSkin_P")
	if lexists(join(g.Paks(), "~mods")) {
		t.Skip("this file system ignores letter case")
	}
	if mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir()); len(mods) != 1 {
		t.Fatalf("Modded: mods %v", keys(mods))
	}
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	if m := g.modeInfo().Mode; m != modeUnmodded || lexists(join(g.Paks(), "~Mods")) {
		t.Fatalf("mode %s, Paks holds %v", m, names(g.Paks()))
	}
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	if mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir()); len(mods) != 1 || g.modeInfo().Mode != modeModded {
		t.Fatalf("Modded again: mods %v", keys(mods))
	}
}

// park-2/park-3: a journal left by launcher 2.0.4 (steps written after each
// move, no "pending") is followed too; one written for another game folder is
// kept aside and not followed.
func TestFixOldAndForeignJournals(t *testing.T) {
	for _, button := range []string{modeModded, modeUnmodded} {
		t.Run(button, func(t *testing.T) {
			g := newGame(t)
			installModding(t, g)
			container(t, join(g.Paks(), "~mods"), "SkinA_P")
			before := treeSnapshot(t, g.Root)
			gp, pp := g.itemPaths(parkItems[1]) // ue4ss
			if err := moveItem(gp, pp); err != nil {
				t.Fatal(err)
			}
			if err := g.writeJournal(&switchJournal{To: modeUnmodded, Steps: []moveStep{{From: gp, To: pp}}}); err != nil {
				t.Fatal(err)
			}
			if s := buildState(); s.UnfinishedSw != modeUnmodded {
				t.Fatalf("unfinished %q", s.UnfinishedSw)
			}
			if _, err := g.switchTo(button); err != nil {
				t.Fatal(err)
			}
			if m := g.modeInfo().Mode; m != button {
				t.Fatalf("mode %s", m)
			}
			if _, err := g.switchTo(modeModded); err != nil {
				t.Fatal(err)
			}
			if after := treeSnapshot(t, g.Root); !reflect.DeepEqual(before, after) {
				t.Fatalf("the game is not as it was:\n%v\n%v", before, after)
			}
		})
	}
	t.Run("other folder", func(t *testing.T) {
		g := newGame(t)
		installModding(t, g)
		elsewhere := join(t.TempDir(), "ACE COMBAT 8")
		if err := g.writeJournal(&switchJournal{To: modeUnmodded, Steps: []moveStep{{From: join(elsewhere, "Game", "x"), To: join(elsewhere, "y")}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := g.switchTo(modeUnmodded); err != nil {
			t.Fatal(err)
		}
		if m := g.modeInfo().Mode; m != modeUnmodded || g.unfinishedSwitch() != "" || !isFile(join(g.Storage(), "switch-journal (other folder).json")) {
			t.Fatalf("mode %s, unfinished %q, storage %v", m, g.unfinishedSwitch(), names(g.Storage()))
		}
	})
}
