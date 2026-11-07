package main

// Regression tests from the final review of the storage folder and the switch:
// the note of a storage move that outlived it, stale copies after a move that
// was cut short, UE4SS.dll tied to UE4SS.log, and copies to another drive that
// were not on the disk yet when their original was deleted.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rsFound: a file with that name is somewhere under one of the roots.
func rsFound(name string, roots ...string) bool {
	for _, root := range roots {
		hit := false
		filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && d.Name() == name {
				hit = true
				return filepath.SkipAll
			}
			return nil
		})
		if hit {
			return true
		}
	}
	return false
}

// rsCutShortWhileDeleting moves the storage folder to target on another drive
// and stops the launcher as it deletes the old Switched off, after the
// settings were saved.
func rsCutShortWhileDeleting(t *testing.T, g Game, target string) {
	t.Helper()
	old := g.Storage()
	toUSB := otherDrive(target)
	t.Cleanup(func() { rename = os.Rename })
	rename = func(a, b string) error {
		if samePath(a, join(old, "Switched off")) && strings.Contains(b, deletingDir) {
			panic("the launcher was stopped")
		}
		return toUSB(a, b)
	}
	func() {
		defer func() { recover() }()
		g.moveStorage(target)
	}()
	rename = toUSB
	if !samePath(g.Storage(), target) {
		t.Fatalf("storage %s, want %s", g.Storage(), target)
	}
}

// storage-note-outlives-commit: the move's note stayed in the new folder while
// it was in use. With the settings lost later, choosing that folder again
// deleted its Switched off, with a mod that was nowhere else.
func TestReviewStorageNoteGoesWhenTheMoveIsSaved(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.Paks(), "~mods"), "SkinA_P")
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	target := join(t.TempDir(), "USB drive", "AC8 storage")
	os.MkdirAll(target, 0o755)
	rsCutShortWhileDeleting(t, g, target)
	if lexists(join(target, movingHere)) {
		t.Errorf("the note of the move is still in the storage folder in use")
	}
	// while Unmodded, a mod added through the launcher goes into Switched off
	container(t, join(g.OffDir(), "Game", "Content", "Paks", "~mods"), "OnlyHere_P")
	// the settings are lost; the player chooses the folder with their mods
	settings.StorageDir, settings.StorageGame = "", ""
	g.moveStorage(target)
	if !rsFound("OnlyHere_P.pak", target, g.DefaultStorage()) {
		t.Fatal("OnlyHere_P, which was only in the chosen folder, was deleted")
	}
}

// storage-note-outlives-commit: a note left in the folder in use (by a launcher
// stopped just after a move was saved, or by an earlier build) is cleared at
// the next switch. Before, pressing "Use the default" while that drive was
// unplugged, then choosing it again, deleted its Switched off.
func TestReviewStorageSwitchClearsALeftNote(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.Paks(), "~mods"), "SkinA_P")
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	old := g.Storage()
	usb := join(t.TempDir(), "USB drive")
	target := join(usb, "AC8 storage")
	os.MkdirAll(target, 0o755)
	rename = otherDrive(target)
	t.Cleanup(func() { rename = os.Rename })
	if _, err := g.moveStorage(target); err != nil {
		t.Fatal(err)
	}
	writeStorageMove(target, storageMove{From: old, Copies: []moveStep{{From: join(old, "Switched off"), To: join(target, "Switched off")}}})

	// the player uses the new folder: Modded, adds a mod, Unmodded
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	if lexists(join(target, movingHere)) {
		t.Errorf("the switch left the note in the storage folder in use")
	}
	container(t, join(g.Paks(), "~mods"), "OnlyHere_P")
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	// the drive is unplugged: "Use the default", pressed twice as the window asks
	os.Rename(usb, usb+" (unplugged)")
	if _, err := g.moveStorage(""); err == nil {
		t.Fatal("the first press should say where the mods are")
	}
	leavingMissing.at = time.Now().Add(-5 * time.Second)
	if _, err := g.moveStorage(""); err != nil {
		t.Fatal(err)
	}
	// sets up modding again and switches Unmodded on the default folder
	installModding(t, g)
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	// the drive is back: the player chooses it to get their mods back
	os.Rename(usb+" (unplugged)", usb)
	g.moveStorage(g.storageTarget(target))
	if !rsFound("OnlyHere_P.pak", target, old) {
		t.Fatal("OnlyHere_P, which was only on the USB drive, was deleted")
	}
}

// storage-note-outlives-commit: a folder taken into use while the old one
// can't be found keeps no note of a move to it.
func TestReviewStorageAdoptedFolderKeepsNoNote(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	gone := join(t.TempDir(), "USB drive", "AC8 storage")
	settings.StorageDir, settings.StorageGame = gone, g.Root
	if !g.storageMissing() {
		t.Fatal("setup: the storage folder should be missing")
	}
	target := join(t.TempDir(), "AC8 storage")
	write(t, join(target, "Removed mods", "SkinA_P", "SkinA_P.pak"), "pak")
	writeStorageMove(target, storageMove{From: gone, Copies: []moveStep{{From: join(gone, "Removed mods", "SkinA_P"), To: join(target, "Removed mods", "SkinA_P")}}})
	if _, err := g.moveStorage(target); err != nil {
		t.Fatal(err)
	}
	if !samePath(g.Storage(), target) {
		t.Fatalf("storage %s", g.Storage())
	}
	if lexists(join(target, movingHere)) {
		t.Fatal("the note of the move is still in the storage folder in use")
	}
}

// storage-stale-copies-kept: a move to another drive is stopped during the
// copy; the player restores a removed mod from the old folder, then chooses
// the new folder again. The stale copy of that mod came back as removed.
func TestReviewStorageStoppedMoveLeavesNoStaleCopies(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.Paks(), "~mods"), "SkinA_P")
	container(t, join(g.Paks(), "~mods"), "SkinB_P")
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	for _, m := range mods {
		if _, err := g.removeToStorage("tilde", m.Key, m.Path, m.IsFile); err != nil {
			t.Fatal(err)
		}
	}
	old := g.Storage()
	target := join(t.TempDir(), "USB drive", "AC8 storage")
	os.MkdirAll(target, 0o755)
	toUSB := otherDrive(target)
	t.Cleanup(func() { rename = os.Rename })
	n := 0
	rename = func(a, b string) error {
		if strings.Contains(a, copyingDir) && !strings.Contains(b, copyingDir) && isInside(b, join(target, "Removed mods")) {
			if n++; n == 2 {
				panic("the launcher was stopped") // the first removed mod is copied
			}
		}
		return toUSB(a, b)
	}
	func() {
		defer func() { recover() }()
		g.moveStorage(target)
	}()
	rename = toUSB
	if !samePath(g.Storage(), old) || len(g.listRemoved()) != 2 {
		t.Fatalf("after the stop: storage %s, removed %d", g.Storage(), len(g.listRemoved()))
	}
	// restore the removed mod that was already copied
	var copied string
	for _, e := range names(join(target, "Removed mods")) {
		if !launcherTemp(e) {
			copied = e
		}
	}
	restored := ""
	for _, r := range g.listRemoved() {
		if copied != "" && strings.Contains(r.ID, copied) {
			restored = r.Name
			if err := g.restoreRemoved(r.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if restored == "" {
		t.Fatalf("setup: no copied removed mod (%q)", copied)
	}
	if _, err := g.moveStorage(target); err != nil {
		t.Fatal(err)
	}
	got := g.listRemoved()
	if len(got) != 1 || got[0].Name == restored {
		t.Fatalf("removed mods after the move: %+v; %s was restored and should not be listed", got, restored)
	}
	if l := leftovers(target); len(l) != 0 || lexists(join(target, movingHere)) {
		t.Fatalf("left over: %v", l)
	}
}

// clashmod-stem-groups-unrelated-files: UE4SS.log (written at every game
// start) was tied to UE4SS.dll, so the older UE4SS.dll came back over a newer
// one copied in by hand while Unmodded.
func TestReviewStorageUE4SSLogDoesNotDecideDll(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	release := time.Now().Add(-60 * 24 * time.Hour)
	os.Chtimes(join(g.Win64(), "ue4ss", "UE4SS.dll"), release, release)
	write(t, join(g.Win64(), "ue4ss", "UE4SS.log"), "last game start")
	yesterday := time.Now().Add(-24 * time.Hour)
	os.Chtimes(join(g.Win64(), "ue4ss", "UE4SS.log"), yesterday, yesterday)
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	p := join(g.Win64(), "ue4ss", "UE4SS.dll")
	write(t, p, "ue4ss-new")
	newer := time.Now().Add(-30 * 24 * time.Hour)
	os.Chtimes(p, newer, newer)
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(join(g.ue4ssRoot(), "UE4SS.dll")); string(b) != "ue4ss-new" {
		t.Fatalf("UE4SS.dll in the game is %q: the newer one should stay", b)
	}
	if b, _ := os.ReadFile(join(g.ue4ssRoot(), "UE4SS.log")); string(b) != "last game start" {
		t.Fatalf("UE4SS.log in the game is %q", b)
	}
}

// cross-drive-no-fsync: a copy to another drive is written to the disk before
// its original is deleted, both in a switch and in a storage move. A copy that
// can't be written is not used, and the original stays.
func TestReviewStorageCrossDriveCopyOnDiskFirst(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.Paks(), "~mods"), "SkinA_P")
	t.Cleanup(func() { rename, syncFile = os.Rename, (*os.File).Sync })
	synced := map[string]bool{}
	syncFile = func(f *os.File) error {
		synced[strings.TrimSuffix(filepath.Base(f.Name()), ".partial")] = true
		return f.Sync()
	}
	var notSynced []string
	deleted := 0
	watch := func(cross func(a, b string) error) func(a, b string) error {
		return func(a, b string) error {
			if strings.Contains(b, deletingDir) {
				deleted++
				filepath.WalkDir(a, func(p string, d fs.DirEntry, err error) error {
					if err == nil && !d.IsDir() && !synced[d.Name()] {
						notSynced = append(notSynced, p)
					}
					return nil
				})
			}
			return cross(a, b)
		}
	}
	// a switch with the storage folder on another drive
	rename = watch(otherDrive(g.Storage()))
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	// a storage move to another drive
	target := join(t.TempDir(), "USB drive", "AC8 storage")
	os.MkdirAll(target, 0o755)
	rename = watch(otherDrive(target))
	if _, err := g.moveStorage(target); err != nil {
		t.Fatal(err)
	}
	if deleted < 2 {
		t.Fatalf("setup: %d originals deleted after a copy", deleted)
	}
	if len(notSynced) > 0 {
		t.Fatalf("deleted before its copy was on the disk: %v", notSynced)
	}
	if g.modeInfo().Mode != modeUnmodded || !rsFound("SkinA_P.pak", g.OffDir()) {
		t.Fatalf("mode %s; Switched off: %v", g.modeInfo().Mode, treeSnapshot(t, g.OffDir()))
	}

	// a drive that can't be asked to write now still gets the copy
	syncFile = func(*os.File) error { return errors.ErrUnsupported }
	rename = otherDrive(g.Storage())
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	// a drive that fails to write it: nothing is changed
	syncFile = func(*os.File) error { return errors.New("input/output error") }
	if _, err := g.switchTo(modeUnmodded); err == nil {
		t.Fatal("the switch should fail when the copy can't be written")
	}
	if m := g.modeInfo().Mode; m != modeModded {
		t.Fatalf("mode %s after a failed switch", m)
	}
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	if !rsFound("SkinA_P.pak", g.Paks()) || len(mods) != 1 {
		t.Fatalf("mods in the game %v", keys(mods))
	}
	if l := leftovers(g.Root); len(l) != 0 {
		t.Fatalf("left in the game: %v", l)
	}
}
