package main

// Regression tests for the final review of adding and updating mods
// (install.go). Each one failed before its fix.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fxHold makes the file p impossible to delete or replace, as a virus scanner
// that has it open does on Windows: through the launcher's removeNew, and with
// chattr +i where the test may (root on Linux), so that plain file calls fail
// as well. It returns the function that lets go.
func fxHold(t *testing.T, p string) func() {
	t.Helper()
	locked := exec.Command("chattr", "+i", p).Run() == nil
	removeNew = func(q string) error {
		if isInside(p, q) {
			return &os.PathError{Op: "unlinkat", Path: p, Err: errors.New("held by another program")}
		}
		return os.RemoveAll(q)
	}
	release := func() {
		removeNew = os.RemoveAll
		if locked {
			exec.Command("chattr", "-i", p).Run()
			locked = false
		}
	}
	t.Cleanup(release)
	return release
}

// fxNoLeftovers fails when the launcher's own temporary names are left in dir.
func fxNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	for _, n := range fxNames(dir) {
		if strings.Contains(n, ".ace8-") {
			t.Fatalf("left behind in %s: %v", dir, fxNames(dir))
		}
	}
}

// commit-fail-marker-dropped: an update whose new copy goes in only in part,
// and whose part can't be taken out again (a scanner holds it), keeps the
// complete old copy and the player's settings, and doesn't say that nothing
// was changed when that isn't so.
func TestReviewFailedUpdateKeepsOldScriptMod(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	v1 := join(t.TempDir(), "CoolCam")
	write(t, join(v1, "Scripts", "main.lua"), "v1")
	if _, err := addFromPaths(g, []string{v1}, nil); err != nil {
		t.Fatal(err)
	}
	target := join(g.ue4ssRoot(), "Mods", "CoolCam")
	write(t, join(target, "config.json"), "my settings") // written by the mod while the game ran
	before := treeSnapshot(t, target)
	v2 := join(t.TempDir(), "CoolCam")
	write(t, join(v2, "Scripts", "main.lua"), "v2")
	release := func() {}
	t.Cleanup(func() { rename = os.Rename })
	rename = func(a, b string) error {
		switch {
		case !strings.Contains(a, "Adding"):
		case samePath(b, target):
			// a virus scanner looks at the new files: they go in one by one
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("The process cannot access the file because it is being used by another process.")}
		case samePath(b, join(target, "config.json")):
			// ... it holds the one that went in first, and this one fails
			release = fxHold(t, join(target, "Scripts", "main.lua"))
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("boom")}
		}
		return os.Rename(a, b)
	}
	_, err := addFromPaths(g, []string{v2}, nil)
	rename = os.Rename
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), "Nothing was changed") && !reflect.DeepEqual(treeSnapshot(t, target), before) {
		t.Fatalf("says nothing was changed, but: %v\n%v", err, treeSnapshot(t, target))
	}
	release()        // the scanner is done
	g.sweepUpdates() // the next start, Play or switch
	if readS(t, join(target, "Scripts", "main.lua")) != "v1" || readS(t, join(target, "config.json")) != "my settings" {
		t.Fatalf("the old version or the player's settings are gone: %v", treeSnapshot(t, target))
	}
	fxNoLeftovers(t, filepath.Dir(target))
}

// commit-fail-marker-dropped, for files that replace files of the same name
// (loose ~mods files, and mods in a folder that holds others): the next
// clean-up prefers the old files over a mix of old and new.
func TestReviewFailedUpdateKeepsOldLooseFiles(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	tm := join(g.Paks(), "~mods")
	write(t, join(tm, "Loose_P.utoc"), "old-toc")
	write(t, join(tm, "Loose_P.ucas"), "old-cas")
	before := treeSnapshot(t, tm)
	z := join(t.TempDir(), "Loose v2.zip")
	makeZip(t, z, map[string]string{"~mods/Loose_P.utoc": "new-toc", "~mods/Loose_P.ucas": "new-cas"})
	release, first := func() {}, ""
	t.Cleanup(func() { rename = os.Rename })
	rename = func(a, b string) error {
		if strings.Contains(a, "Adding") && samePath(filepath.Dir(b), tm) {
			if first == "" {
				first = b
				return os.Rename(a, b)
			}
			release = fxHold(t, first) // a scanner holds the file that went in first
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("boom")}
		}
		return os.Rename(a, b)
	}
	_, err := addFromPaths(g, []string{z}, nil)
	rename = os.Rename
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), "Nothing was changed") && !reflect.DeepEqual(treeSnapshot(t, tm), before) {
		t.Fatalf("says nothing was changed, but: %v\n%v", err, treeSnapshot(t, tm))
	}
	release()
	g.sweepUpdates()
	if got := treeSnapshot(t, tm); !reflect.DeepEqual(got, before) {
		t.Fatalf("not the old files after the clean-up: %v", got)
	}
}

// add-renamed-containers-dup: an update whose file names carry the version
// (HornetSkin_v1_P, then HornetSkin_v11_P) updates the folder of the same name
// when its .utoc changes the same game files, instead of going next to it as
// "Hornet Skin (2)" with both loading. The old copy is kept in Removed mods,
// in case it was a different mod with the same folder name.
func TestReviewRenamedContainersUpdateTheSameFolder(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	ld := g.loaderDir()
	z1 := join(t.TempDir(), "Hornet Skin v1.0.zip")
	makeZip(t, z1, map[string]string{"Hornet Skin/HornetSkin_v1_P.utoc": string(fxUtoc([]uint64{101, 102, 103}, nil, nil)), "Hornet Skin/HornetSkin_v1_P.ucas": "v1"})
	if _, err := addFromPaths(g, []string{z1}, nil); err != nil {
		t.Fatal(err)
	}
	container(t, join(ld, "Zeta"), "Zeta_P")
	write(t, join(ld, "loadorder.txt"), "-Hornet Skin\r\n+Zeta\r\n")
	z2 := join(t.TempDir(), "Hornet Skin v1.1.zip")
	makeZip(t, z2, map[string]string{"Hornet Skin/HornetSkin_v11_P.utoc": string(fxUtoc([]uint64{101, 102}, nil, nil)), "Hornet Skin/HornetSkin_v11_P.ucas": "v11"})
	res, err := addFromPaths(g, []string{z2}, nil)
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"Hornet Skin"}) || len(res.Added) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := fxNames(join(ld, "Hornet Skin")); !reflect.DeepEqual(got, []string{"HornetSkin_v11_P.ucas", "HornetSkin_v11_P.utoc"}) || exists(join(ld, "Hornet Skin (2)")) {
		t.Fatalf("Hornet Skin holds %v; folders %v", got, fxNames(ld))
	}
	if got := fxOrder(g); !reflect.DeepEqual(got, []string{"-Hornet Skin", "+Zeta"}) {
		t.Fatalf("order %v", got)
	}
	rm := g.listRemoved()
	if len(rm) != 1 || readS(t, join(g.RemovedDir(), rm[0].ID, "Hornet Skin", "HornetSkin_v1_P.ucas")) != "v1" || !strings.Contains(strings.Join(res.Notes, " "), "Removed mods") {
		t.Fatalf("the old copy was not kept: %+v, notes %v", rm, res.Notes)
	}
	fxNoLeftovers(t, ld)

	// a mod with the same folder name that changes other game files is a different mod
	z3 := join(t.TempDir(), "Red Hornet.zip")
	makeZip(t, z3, map[string]string{"Hornet Skin/RedHornet_P.utoc": string(fxUtoc([]uint64{900}, nil, nil)), "Hornet Skin/RedHornet_P.ucas": "red"})
	res, err = addFromPaths(g, []string{z3}, nil)
	if err != nil || !reflect.DeepEqual(res.Added, []string{"Hornet Skin (2)"}) || !isFile(join(ld, "Hornet Skin", "HornetSkin_v11_P.utoc")) {
		t.Fatalf("%+v %v", res, err)
	}

	// in a folder that holds other mods too (here in ~mods), only the old
	// version's files are replaced, and they can be restored from Removed mods
	tm := join(g.Paks(), "~mods")
	write(t, join(tm, "Skins", "F22Red_P.utoc"), string(fxUtoc([]uint64{1, 2}, nil, nil)))
	write(t, join(tm, "Skins", "F22Red_P.ucas"), "red")
	write(t, join(tm, "Skins", "Su57_P.utoc"), string(fxUtoc([]uint64{50}, nil, nil)))
	write(t, join(tm, "Skins", "Su57_P.ucas"), "su")
	z4 := join(t.TempDir(), "F-22 Blue.zip")
	makeZip(t, z4, map[string]string{"~mods/Skins/F22Blue_P.utoc": string(fxUtoc([]uint64{1, 2}, nil, nil)), "~mods/Skins/F22Blue_P.ucas": "blue"})
	res, err = addFromPaths(g, []string{z4}, nil)
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"Skins"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if got := fxNames(join(tm, "Skins")); !reflect.DeepEqual(got, []string{"F22Blue_P.ucas", "F22Blue_P.utoc", "Su57_P.ucas", "Su57_P.utoc"}) || exists(join(tm, "Skins (2)")) {
		t.Fatalf("Skins holds %v; ~mods %v", got, fxNames(tm))
	}
	var id string
	for _, r := range g.listRemoved() {
		if strings.Contains(r.Name, "F22Red_P") {
			id = r.ID
		}
	}
	if id == "" {
		t.Fatalf("not in Removed mods: %+v", g.listRemoved())
	}
	if err := g.restoreRemoved(id); err != nil || readS(t, join(tm, "Skins", "F22Red_P.ucas")) != "red" {
		t.Fatalf("restore: %v, Skins holds %v", err, fxNames(join(tm, "Skins")))
	}
	fxNoLeftovers(t, join(tm, "Skins"))
}

// logicmods-no-older-copies: picking the other version of a LogicMods mod
// later, or an update in a folder with a new name, replaces the copy in
// LogicMods instead of adding a second one that loads as well.
func TestReviewLogicModsOlderCopyReplaced(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	write(t, join(g.Win64(), "ue4ss", "Mods", "BPModLoaderMod", "Scripts", "main.lua"), "x")
	logic := join(g.Paks(), "LogicMods")
	z := join(t.TempDir(), "Thing.zip")
	makeZip(t, z, map[string]string{
		"LogicMods/Fast/Thing.pak": "fast", "LogicMods/Fast/Thing.ucas": "c", "LogicMods/Fast/Thing.utoc": "t",
		"LogicMods/Slow/Thing.pak": "slow", "LogicMods/Slow/Thing.ucas": "c", "LogicMods/Slow/Thing.utoc": "t",
	})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil || len(res.Choices) != 2 {
		t.Fatalf("no choice: %+v %v", res, err)
	}
	ids := map[string]string{}
	for _, c := range res.Choices {
		ids[c.Name] = c.ID
	}
	if _, err := addFromPaths(g, []string{z}, []string{ids["Fast"]}); err != nil {
		t.Fatal(err)
	}
	res, err = addFromPaths(g, []string{z}, []string{ids["Slow"]})
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"Thing - Slow"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if got := fxNames(logic); !reflect.DeepEqual(got, []string{"Thing - Slow"}) || readS(t, join(logic, "Thing - Slow", "Thing.pak")) != "slow" {
		t.Fatalf("LogicMods holds %v", got)
	}
	// an update in a folder with a new name
	src := join(t.TempDir(), "Thing v2", "LogicMods", "Thing v2")
	write(t, join(src, "Thing.pak"), "v2")
	if res, err = addFromPaths(g, []string{filepath.Dir(filepath.Dir(src))}, nil); err != nil || len(res.Updated) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := fxNames(logic); !reflect.DeepEqual(got, []string{"Thing v2"}) {
		t.Fatalf("LogicMods holds %v", got)
	}
}

// partial-replace-turns-mod-on: an update that replaces only part of a
// switched-off mod (it holds another container too) stays switched off, right
// after it in the load order.
func TestReviewPartialReplaceKeepsModOff(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	ld := g.loaderDir()
	container(t, join(ld, "Hornet v1"), "Hornet_P")
	container(t, join(ld, "Hornet v1"), "HornetExtra_P")
	container(t, join(ld, "Zeta"), "Zeta_P")
	write(t, join(ld, "loadorder.txt"), "-Hornet v1\r\n+Zeta\r\n")
	src := join(t.TempDir(), "Hornet v2")
	container(t, src, "Hornet_P")
	res, err := addFromPaths(g, []string{src}, nil)
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"Hornet v2"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if got := fxOrder(g); !reflect.DeepEqual(got, []string{"-Hornet v1", "-Hornet v2", "+Zeta"}) {
		t.Fatalf("order %v", got)
	}
	if got := fxNames(join(ld, "Hornet v1")); !reflect.DeepEqual(got, []string{"HornetExtra_P.pak", "HornetExtra_P.ucas", "HornetExtra_P.utoc"}) {
		t.Fatalf("Hornet v1 holds %v", got)
	}
}

// scriptmodoff-old-rule: an update keeps a script mod on or off as the Script
// mods list shows it, which is how UE4SS 3.0.1 runs it: enabled.txt starts it
// whatever mods.txt says, and mods.txt names it in its own capitals.
func TestReviewScriptUpdateKeepsListedState(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	md := join(g.ue4ssRoot(), "Mods")
	write(t, join(md, "mods.txt"), "IoStoreLoaderMod : 1\r\nSkyLive : 0\r\nskycam : 1\r\n\r\n; Built-in keybinds, do not move up!\r\nKeybinds : 1\r\n")
	write(t, join(md, "SkyLive", "Scripts", "main.lua"), "-- v1")
	write(t, join(md, "SkyLive", "enabled.txt"), "") // on: UE4SS runs it
	write(t, join(md, "SkyCam", "Scripts", "main.lua"), "-- v1")
	// off: no enabled.txt, and "skycam" is not UE4SS's name for it
	if on := fxScriptsOn(g); !reflect.DeepEqual(on, []string{"SkyLive"}) {
		t.Fatalf("before: on %v", on)
	}
	for _, name := range []string{"SkyLive", "SkyCam"} {
		z := join(t.TempDir(), name+" v2.zip")
		makeZip(t, z, map[string]string{name + "/Scripts/main.lua": "-- v2"})
		if res, err := addFromPaths(g, []string{z}, nil); err != nil || len(res.Updated) != 1 {
			t.Fatalf("%+v %v", res, err)
		}
		if readS(t, join(md, name, "Scripts", "main.lua")) != "-- v2" {
			t.Fatalf("%s not updated", name)
		}
	}
	if on := fxScriptsOn(g); !reflect.DeepEqual(on, []string{"SkyLive"}) {
		t.Fatalf("after the updates: on %v", on)
	}
}
