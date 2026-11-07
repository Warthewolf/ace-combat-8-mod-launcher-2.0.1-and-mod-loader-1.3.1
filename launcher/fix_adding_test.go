package main

// Regression tests for adding and updating mods (install.go, update_merge.go,
// moddates.go). Each one failed before its fix.

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func fxNames(dir string) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func fxOrder(g Game) []string {
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	var out []string
	for _, m := range mods {
		out = append(out, map[bool]string{true: "+", false: "-"}[m.Enabled]+m.Key)
	}
	return out
}

func fxScriptsOn(g Game) []string {
	var on []string
	for _, m := range listScriptMods(g.ue4ssRoot()) {
		if m.Enabled {
			on = append(on, m.Name)
		}
	}
	return on
}

// inst-1: updating one mod that sits in a folder with others (a ~mods folder
// the player made, a pack) replaces only that mod; the others stay.
func TestFixUpdateKeepsOtherModsInTheFolder(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	skins := join(g.Paks(), "~mods", "Skins")
	for _, b := range []string{"F22_Grey_P", "Su57_Navy_P", "F15_Eagle_P"} {
		container(t, skins, b)
	}
	z := join(t.TempDir(), "Su-57 Navy v2.zip")
	makeZip(t, z, map[string]string{"Su57_Navy_P.utoc": "toc2", "Su57_Navy_P.ucas": "cas2", "Su57_Navy_P.pak": "pak2"})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"F15_Eagle_P.pak", "F15_Eagle_P.ucas", "F15_Eagle_P.utoc", "F22_Grey_P.pak", "F22_Grey_P.ucas", "F22_Grey_P.utoc"}
	if got := fxNames(skins); !reflect.DeepEqual(got, want) {
		t.Fatalf("~mods\\Skins holds %v; %+v", got, res)
	}
	if b := readS(t, join(g.loaderDir(), "Su-57 Navy v2", "Su57_Navy_P.ucas")); b != "cas2" {
		t.Fatalf("new copy %q", b)
	}
	if !strings.Contains(strings.Join(res.Notes, " "), `The other mods in ~mods\Skins were kept`) {
		t.Fatalf("notes %v", res.Notes)
	}

	// a pack in the Mod Loader's folder, each skin in a folder of its own
	pack := join(g.loaderDir(), "All Su-57 Skins")
	for _, b := range []string{"Su57_A_P", "Su57_B_P", "Su57_C_P"} {
		container(t, join(pack, b), b)
	}
	src := join(t.TempDir(), "Su-57 B v2")
	container(t, src, "Su57_B_P")
	if _, err := addFromPaths(g, []string{src}, nil); err != nil {
		t.Fatal(err)
	}
	if got := fxNames(pack); !reflect.DeepEqual(got, []string{"Su57_A_P", "Su57_C_P"}) {
		t.Fatalf("the pack holds %v", got)
	}
	if !isFile(join(g.loaderDir(), "Su-57 B v2", "Su57_B_P.utoc")) {
		t.Fatal("the new copy is not in")
	}

	// the player's backup of something else, in a folder the Mod Loader skips, stays
	hornet := join(g.loaderDir(), "Hornet")
	container(t, hornet, "Hornet_P")
	container(t, join(hornet, "backup"), "OldThing_P")
	src2 := join(t.TempDir(), "Hornet v2")
	container(t, src2, "Hornet_P")
	if _, err := addFromPaths(g, []string{src2}, nil); err != nil {
		t.Fatal(err)
	}
	if !isFile(join(hornet, "backup", "OldThing_P.utoc")) || isFile(join(hornet, "Hornet_P.utoc")) {
		t.Fatalf("Hornet holds %v", treeSnapshot(t, hornet))
	}
	if len(g.listRemoved()) != 0 {
		t.Fatalf("removed %+v", g.listRemoved())
	}
	// a mod whose files the new copy all has is still replaced as a whole
	container(t, join(g.loaderDir(), "Zeta old"), "Zeta_P")
	src3 := join(t.TempDir(), "Zeta")
	container(t, src3, "Zeta_P")
	if _, err := addFromPaths(g, []string{src3}, nil); err != nil {
		t.Fatal(err)
	}
	if exists(join(g.loaderDir(), "Zeta old")) {
		t.Fatal("the older copy was not replaced")
	}
}

// inst-1, inst-2: a download laid out as ~mods\Skins, going into the player's
// ~mods\Skins that holds other skins too, replaces only its own files.
func TestFixSameFolderWithOtherModsIsMerged(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	skins := join(g.Paks(), "~mods", "Skins")
	container(t, skins, "F22_Grey_P")
	container(t, skins, "Su57_Navy_P")
	write(t, join(skins, "readme.txt"), "the player's")
	z := join(t.TempDir(), "navy.zip")
	makeZip(t, z, map[string]string{"~mods/Skins/Su57_Navy_P.utoc": "toc2", "~mods/Skins/Su57_Navy_P.ucas": "cas2", "~mods/Skins/readme.txt": "the mod's"})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !isFile(join(skins, "F22_Grey_P.utoc")) || readS(t, join(skins, "Su57_Navy_P.ucas")) != "cas2" || exists(join(skins, "Su57_Navy_P.pak")) {
		t.Fatalf("Skins holds %v; %+v", fxNames(skins), res)
	}
	if readS(t, join(skins, "readme.txt")) != "the player's" {
		t.Fatal("the folder's own file was replaced")
	}
	if !reflect.DeepEqual(res.Updated, []string{"Skins"}) || !strings.Contains(strings.Join(res.Notes, " "), "F22_Grey_P") {
		t.Fatalf("%+v", res)
	}
	if left := fxNames(skins); len(left) != 6 {
		t.Fatalf("left behind: %v", left)
	}
}

// inst-2: a different mod with the same folder name is added under a new name;
// the first one stays.
func TestFixSameNameDifferentModIsKept(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	// loose _P.pak files are named after themselves, not all "New mod"
	d1, d2 := t.TempDir(), t.TempDir()
	write(t, join(d1, "Font_P.pak"), "font")
	write(t, join(d2, "Other_P.pak"), "other")
	if _, err := addFromPaths(g, []string{join(d1, "Font_P.pak")}, nil); err != nil {
		t.Fatal(err)
	}
	res, err := addFromPaths(g, []string{join(d2, "Other_P.pak")}, nil)
	if err != nil || !reflect.DeepEqual(res.Added, []string{"Other_P"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if !isFile(join(g.loaderDir(), "Font_P", "Font_P.pak")) {
		t.Fatal("Font_P.pak was replaced by an unrelated mod")
	}
	// a pak folder with a name that says nothing ("Paks") is named after the download
	z1, z2 := join(t.TempDir(), "F-22 Skin.zip"), join(t.TempDir(), "Better Clouds.zip")
	makeZip(t, z1, map[string]string{"Paks/F22Skin_P.utoc": "t", "Paks/F22Skin_P.ucas": "c"})
	makeZip(t, z2, map[string]string{"Paks/Clouds_P.utoc": "t", "Paks/Clouds_P.ucas": "c"})
	r1, e1 := addFromPaths(g, []string{z1}, nil)
	r2, e2 := addFromPaths(g, []string{z2}, nil)
	if e1 != nil || e2 != nil || !reflect.DeepEqual(r1.Added, []string{"F-22 Skin"}) || !reflect.DeepEqual(r2.Added, []string{"Better Clouds"}) {
		t.Fatalf("%+v %v / %+v %v", r1, e1, r2, e2)
	}
	// the same folder name with other files: a new name, and a note
	a, b := join(t.TempDir(), "Skin"), join(t.TempDir(), "Skin")
	container(t, a, "Raptor_P")
	container(t, b, "Eagle_P")
	if _, err := addFromPaths(g, []string{a}, nil); err != nil {
		t.Fatal(err)
	}
	res, err = addFromPaths(g, []string{b}, nil)
	if err != nil || !reflect.DeepEqual(res.Added, []string{"Skin (2)"}) || !strings.Contains(strings.Join(res.Notes, " "), "added as Skin (2)") {
		t.Fatalf("%+v %v", res, err)
	}
	if !isFile(join(g.loaderDir(), "Skin", "Raptor_P.utoc")) || !isFile(join(g.loaderDir(), "Skin (2)", "Eagle_P.utoc")) {
		t.Fatal("wrong folders")
	}
	// a newer version of the second one updates "Skin (2)" in place
	write(t, join(b, "Eagle_P.ucas"), "v2")
	res, err = addFromPaths(g, []string{b}, nil)
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"Skin (2)"}) || readS(t, join(g.loaderDir(), "Skin (2)", "Eagle_P.ucas")) != "v2" {
		t.Fatalf("%+v %v", res, err)
	}
	if !isFile(join(g.loaderDir(), "Skin", "Raptor_P.utoc")) || exists(join(g.loaderDir(), "Skin (3)")) {
		t.Fatal("wrong folders after the update")
	}
	// the same mod again is an update, as before
	res, err = addFromPaths(g, []string{a}, nil)
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"Skin"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if len(g.listRemoved()) != 0 {
		t.Fatalf("removed %+v", g.listRemoved())
	}
}

// inst-3: a LogicMods mod as .pak + .ucas + .utoc goes in whole.
func TestFixLogicModsTrio(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	logic := join(g.Paks(), "LogicMods")
	write(t, join(logic, "Thing.pak"), "old")
	write(t, join(logic, "Thing.ucas"), "old")
	write(t, join(logic, "Thing.utoc"), "old")
	write(t, join(logic, "Other.pak"), "other")
	z := join(t.TempDir(), "BP mod.zip")
	makeZip(t, z, map[string]string{"LogicMods/Thing.pak": "p", "LogicMods/Thing.ucas": "c", "LogicMods/Thing.utoc": "t"})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"Thing"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if got := fxNames(logic); !reflect.DeepEqual(got, []string{"Other.pak", "Thing.pak", "Thing.ucas", "Thing.utoc"}) {
		t.Fatalf("LogicMods holds %v", got)
	}
	if readS(t, join(logic, "Thing.ucas")) != "c" {
		t.Fatal("not the new copy")
	}
}

// inst-4: a download that brings UE4SS's own mods keeps the player's copies, and
// a mod switched off by hand in mods.txt stays off when it is updated.
func TestFixBundledUE4SSModsAndOffState(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	md := join(g.ue4ssRoot(), "Mods")
	write(t, join(md, "mods.txt"), "IoStoreLoaderMod : 1\r\nSplitScreenMod : 0\r\nHandOff : 0\r\n\r\n; Built-in keybinds, do not move up!\r\nKeybinds : 1\r\n")
	write(t, join(md, "SplitScreenMod", "Scripts", "main.lua"), "-- old")
	write(t, join(md, "HandOff", "Scripts", "main.lua"), "-- old")
	write(t, join(md, "HandOff", "enabled.txt"), "") // enabled.txt, but "HandOff : 0": off
	z := join(t.TempDir(), "Cool Mod with UE4SS.zip")
	makeZip(t, z, map[string]string{"ue4ss/Mods/SplitScreenMod/Scripts/main.lua": "-- new", "ue4ss/Mods/HandOff/Scripts/main.lua": "-- new"})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"HandOff (script mod)"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if readS(t, join(md, "SplitScreenMod", "Scripts", "main.lua")) != "-- old" || !strings.Contains(strings.Join(res.Notes, " "), "SplitScreenMod") {
		t.Fatalf("UE4SS's own mod was replaced; notes %v", res.Notes)
	}
	if readS(t, join(md, "HandOff", "Scripts", "main.lua")) != "-- new" {
		t.Fatal("not updated")
	}
	if on := fxScriptsOn(g); !reflect.DeepEqual(on, []string{"Keybinds"}) && len(on) != 0 {
		t.Fatalf("switched on: %v", on)
	}
	for _, m := range listScriptMods(g.ue4ssRoot()) {
		if m.Name == "HandOff" && m.Enabled {
			t.Fatal("HandOff was switched on by the update")
		}
	}
}

// inst-5: an update in a folder with another name keeps the old one's place in
// the load order, its on/off state and the date it was first added.
func TestFixRenamedUpdateKeepsPlaceAndState(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	v1 := join(t.TempDir(), "Hornet v1")
	container(t, v1, "Hornet_P")
	if _, err := addFromPaths(g, []string{v1}, nil); err != nil {
		t.Fatal(err)
	}
	container(t, join(g.loaderDir(), "Zeta"), "Zeta_P")
	write(t, join(g.loaderDir(), "loadorder.txt"), "-Hornet v1\r\n+Zeta\r\n")
	added := loadModDates()[pakDateKey("Hornet v1")].Added
	if added.IsZero() {
		t.Fatal("no date")
	}
	time.Sleep(20 * time.Millisecond)
	v2 := join(t.TempDir(), "Hornet v2")
	container(t, v2, "Hornet_P")
	res, err := addFromPaths(g, []string{v2}, nil)
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"Hornet v2"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if got := fxOrder(g); !reflect.DeepEqual(got, []string{"-Hornet v2", "+Zeta"}) {
		t.Fatalf("order %v", got)
	}
	d := loadModDates()[pakDateKey("Hornet v2")]
	if !d.Added.Equal(added) || !d.Updated.After(added) {
		t.Fatalf("dates %+v, first added %v", d, added)
	}
}

// inst-6: versions laid out as ~mods or ue4ss folders are offered as a choice,
// and only the one picked goes in.
func TestFixAlternativesInPlaceLayouts(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	z := join(t.TempDir(), "HUD colours.zip")
	makeZip(t, z, map[string]string{
		"Option A - Green/~mods/HUD_P.utoc": "t", "Option A - Green/~mods/HUD_P.ucas": "green",
		"Option B - Amber/~mods/HUD_P.utoc": "t", "Option B - Amber/~mods/HUD_P.ucas": "amber",
		"Option A - Green/ue4ss/Mods/IoStoreLoaderMod/Radar/Radar_P.utoc": "t", "Option A - Green/ue4ss/Mods/IoStoreLoaderMod/Radar/Radar_P.ucas": "green",
		"Option B - Amber/ue4ss/Mods/IoStoreLoaderMod/Radar/Radar_P.utoc": "t", "Option B - Amber/ue4ss/Mods/IoStoreLoaderMod/Radar/Radar_P.ucas": "amber",
	})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil || len(res.Choices) != 2 || res.Choices[0].Name != "Option A - Green" || res.Choices[1].Name != "Option B - Amber" || len(res.Added) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if exists(join(g.Paks(), "~mods")) || exists(join(g.loaderDir(), "Radar")) {
		t.Fatal("something went in before the choice")
	}
	res, err = addFromPaths(g, []string{z}, []string{res.Choices[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if readS(t, join(g.Paks(), "~mods", "HUD_P.ucas")) != "amber" || readS(t, join(g.loaderDir(), "Radar", "Radar_P.ucas")) != "amber" {
		t.Fatalf("%+v", res)
	}
	// parts of one mod in several folders, with no file in two of them, are one mod
	z2 := join(t.TempDir(), "parts.zip")
	makeZip(t, z2, map[string]string{"Part 1/~mods/Pack/A_P.utoc": "t", "Part 1/~mods/Pack/A_P.ucas": "c", "Part 2/~mods/Pack/B_P.utoc": "t", "Part 2/~mods/Pack/B_P.ucas": "c"})
	res, err = addFromPaths(g, []string{z2}, nil)
	if err != nil || len(res.Choices) != 0 || !reflect.DeepEqual(res.Added, []string{"Pack"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if !isFile(join(g.Paks(), "~mods", "Pack", "A_P.utoc")) || !isFile(join(g.Paks(), "~mods", "Pack", "B_P.utoc")) {
		t.Fatal("parts missing")
	}
}

// inst-7: the pak part of a script mod goes where the Mod Loader loads it.
func TestFixPakInsideScriptModFolder(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	z := join(t.TempDir(), "Radar.zip")
	makeZip(t, z, map[string]string{"Radar/Scripts/main.lua": "print(1)", "Radar/config.json": "{}", "Radar/Radar_P.utoc": "t", "Radar/Radar_P.ucas": "c"})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(res.Added)
	if !reflect.DeepEqual(res.Added, []string{"Radar", "Radar (script mod)"}) {
		t.Fatalf("%+v", res)
	}
	if !isFile(join(g.loaderDir(), "Radar", "Radar_P.ucas")) || exists(join(g.ue4ssRoot(), "Mods", "Radar", "Radar_P.utoc")) || !isFile(join(g.ue4ssRoot(), "Mods", "Radar", "config.json")) {
		t.Fatal("files in the wrong place")
	}
	// Scripts\main.lua at the top of the zip: the zip's pak folder is a pak mod too
	z2 := join(t.TempDir(), "Combo.zip")
	makeZip(t, z2, map[string]string{"Scripts/main.lua": "print(1)", "Assets/Combo_P.utoc": "t", "Assets/Combo_P.ucas": "c"})
	if _, err := addFromPaths(g, []string{z2}, nil); err != nil {
		t.Fatal(err)
	}
	if !isFile(join(g.loaderDir(), "Combo", "Combo_P.utoc")) || exists(join(g.ue4ssRoot(), "Mods", "Combo", "Assets")) {
		t.Fatalf("%v", treeSnapshot(t, g.loaderDir()))
	}
}

// inst-8: a second download of a script mod ("CoolCam (1).zip", "CoolCam-1.3.0")
// updates it instead of adding a second copy that runs too.
func TestFixScriptModSecondDownload(t *testing.T) {
	for in, want := range map[string]string{"CoolCam (1)": "CoolCam", "CoolCam-1.2.0": "CoolCam", "CoolCam v1.3": "CoolCam", "CoolCam_v2": "CoolCam",
		"CoolCam-1.2.0 (2)": "CoolCam", "Mod 2": "Mod 2", "RoGLaser": "RoGLaser", "1.2.0": "1.2.0"} {
		if got := baseModName(in); got != want {
			t.Errorf("baseModName(%q) = %q", in, got)
		}
	}
	g := newGame(t)
	installModding(t, g)
	z1, z2 := join(t.TempDir(), "CoolCam.zip"), join(t.TempDir(), "CoolCam (1).zip")
	makeZip(t, z1, map[string]string{"Scripts/main.lua": "-- v1"})
	makeZip(t, z2, map[string]string{"Scripts/main.lua": "-- v2"})
	if _, err := addFromPaths(g, []string{z1}, nil); err != nil {
		t.Fatal(err)
	}
	res, err := addFromPaths(g, []string{z2}, nil)
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"CoolCam (script mod)"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if on := fxScriptsOn(g); !reflect.DeepEqual(on, []string{"CoolCam", "Keybinds"}) && !reflect.DeepEqual(on, []string{"CoolCam"}) {
		t.Fatalf("on: %v", on)
	}
	// a versioned top folder; one installed before this fix keeps its folder name
	g = newGame(t)
	installModding(t, g)
	write(t, join(g.ue4ssRoot(), "Mods", "Cam-1.2.0", "Scripts", "main.lua"), "-- v1")
	write(t, join(g.ue4ssRoot(), "Mods", "Cam-1.2.0", "enabled.txt"), "")
	b := join(t.TempDir(), "Cam-1.3.0")
	write(t, join(b, "Scripts", "main.lua"), "-- v2")
	res, err = addFromPaths(g, []string{b}, nil)
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"Cam-1.2.0 (script mod)"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if readS(t, join(g.ue4ssRoot(), "Mods", "Cam-1.2.0", "Scripts", "main.lua")) != "-- v2" || exists(join(g.ue4ssRoot(), "Mods", "Cam-1.3.0")) {
		t.Fatal("not updated in place")
	}
}

// inst-9: a pak folder named like one the Mod Loader skips is named after the
// folder above it, so the mod loads and shows on the Mods page.
func TestFixSkippedFolderName(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	z := join(t.TempDir(), "Raptor Livery.zip")
	makeZip(t, z, map[string]string{"Raptor Livery/Packed/Raptor_P.utoc": "t", "Raptor Livery/Packed/Raptor_P.ucas": "c"})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil || !reflect.DeepEqual(res.Added, []string{"Raptor Livery"}) {
		t.Fatalf("%+v %v", res, err)
	}
	z2 := join(t.TempDir(), "Eagle.zip")
	makeZip(t, z2, map[string]string{"~mods/Old/Eagle_P.utoc": "t", "~mods/Old/Eagle_P.ucas": "c"})
	if _, err := addFromPaths(g, []string{z2}, nil); err != nil {
		t.Fatal(err)
	}
	if got := keys(mustScan(g)); !reflect.DeepEqual(got, []string{"Raptor Livery", `~mods\Eagle`}) {
		t.Fatalf("listed %v", got)
	}
}

func mustScan(g Game) []*PakMod {
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	return mods
}

// inst-12: install files without UE4SS.dll are not reported as set up.
func TestFixSetupWithoutUE4SSDll(t *testing.T) {
	g := newGame(t)
	bundle(t, "1.3.1")
	os.Remove(join(filepath.Dir(exeDir), "War’s Mod Loader 1.3.1", "Game", "Binaries", "Win64", "ue4ss", "UE4SS.dll"))
	bundleAt = time.Time{}
	before := treeSnapshot(t, g.Root)
	_, err := g.setup()
	if err == nil || !strings.Contains(err.Error(), "UE4SS.dll is missing") || !strings.Contains(err.Error(), "antivirus") {
		t.Fatalf("err %v", err)
	}
	if after := treeSnapshot(t, g.Root); !reflect.DeepEqual(before, after) {
		t.Fatal("the game was changed")
	}
	// with UE4SS already in the game, the same files update the Mod Loader fine
	g2 := newGame(t)
	installModding(t, g2)
	bundle(t, "1.3.1")
	os.Remove(join(filepath.Dir(exeDir), "War’s Mod Loader 1.3.1", "Game", "Binaries", "Win64", "ue4ss", "UE4SS.dll"))
	bundleAt = time.Time{}
	if _, err := g2.setup(); err != nil || g2.setupStatus().Loader != "1.3.1" {
		t.Fatalf("err %v", err)
	}
	// a file that does not arrive is named
	g3 := newGame(t)
	bundle(t, "1.3.1")
	if _, err := g3.setup(); err != nil {
		t.Fatal(err)
	}
	os.Remove(join(g3.ue4ssRoot(), "UE4SS.dll"))
	os.Remove(join(filepath.Dir(exeDir), "War’s Mod Loader 1.3.1", "Game", "Binaries", "Win64", "ue4ss", "UE4SS.dll"))
	bundleAt = time.Time{}
	if _, err := g3.setup(); err == nil {
		t.Fatal("no error")
	}
}

// inst-13: the game folder itself is never taken as the Mod Loader download.
func TestFixBundleIsNotTheGame(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	exeDir = join(filepath.Dir(g.Root), "ACE8 Mod Launcher 2.0.4")
	os.MkdirAll(exeDir, 0o755)
	bundle(t, "1.1.0")
	bundleAt = time.Time{}
	if bd := bundleDir(); samePath(bd, g.Root) || !strings.Contains(bd, "Mod Loader 1.1.0") {
		t.Fatalf("bundleDir %s", bd)
	}
	// the launcher in the game folder itself
	exeDir = g.Root
	bundleAt = time.Time{}
	if bd := bundleDir(); samePath(bd, g.Root) || !strings.Contains(bd, "Mod Loader 1.1.0") {
		t.Fatalf("bundleDir %s", bd)
	}
	os.RemoveAll(join(filepath.Dir(g.Root), "War’s Mod Loader 1.1.0"))
	bundleAt = time.Time{}
	if bd := bundleDir(); bd != "" {
		t.Fatalf("bundleDir %s", bd)
	}
	bundleAt = time.Time{}
}

// inst-14: a zip that holds only another archive says so.
func TestFixNestedArchiveMessage(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	z := join(t.TempDir(), "Outer.zip")
	makeZip(t, z, map[string]string{"Mod v1.2.zip": "PK..."})
	_, err := addFromPaths(g, []string{z}, nil)
	if err == nil || !strings.Contains(err.Error(), "another archive inside (Mod v1.2.zip)") {
		t.Fatalf("err %v", err)
	}
}

// inst-15: "Mods\shared\…" without the ue4ss folder above it is UE4SS's shared folder.
func TestFixModsSharedLayout(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	z := join(t.TempDir(), "SimpleAudioPlayer.zip")
	makeZip(t, z, map[string]string{"Mods/SimpleAudioPlayer/Scripts/main.lua": "require('SimpleAudio.lib')", "Mods/shared/SimpleAudio/lib.lua": "return {}"})
	if _, err := addFromPaths(g, []string{z}, nil); err != nil {
		t.Fatal(err)
	}
	if !isFile(join(g.ue4ssRoot(), "Mods", "shared", "SimpleAudio", "lib.lua")) || !isFile(join(g.ue4ssRoot(), "Mods", "SimpleAudioPlayer", "Scripts", "main.lua")) {
		t.Fatalf("%v", treeSnapshot(t, join(g.ue4ssRoot(), "Mods")))
	}
}

// inst-17: a launcher closed while a new copy was going in (file by file)
// brings back the complete old copy, not half the new one.
func TestFixSweepPrefersCompleteOldCopy(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	ld := g.loaderDir()
	write(t, join(ld, "Hornet", "Hornet_P.utoc"), "new")        // only part of the new copy
	container(t, join(ld, "Hornet"+updatingSuffix), "Hornet_P") // the complete old copy
	write(t, join(ld, "Hornet"+incompleteSuffix), "")           // the new one was still going in
	write(t, join(ld, "Fresh"+incompleteSuffix), "")            // no old copy: the mod stays as it is
	container(t, join(ld, "Fresh"), "Fresh_P")
	write(t, join(ld, "Pack", "Skin", "Skin_P.utoc"+updatingSuffix), "old") // a file taken out of a pack
	sweepUpdating(ld)
	if got := fxNames(join(ld, "Hornet")); !reflect.DeepEqual(got, []string{"Hornet_P.pak", "Hornet_P.ucas", "Hornet_P.utoc"}) || readS(t, join(ld, "Hornet", "Hornet_P.utoc")) != "toc" {
		t.Fatalf("Hornet holds %v", got)
	}
	if exists(join(ld, "Hornet"+updatingSuffix)) || exists(join(ld, "Hornet"+incompleteSuffix)) || exists(join(ld, "Fresh"+incompleteSuffix)) || !isFile(join(ld, "Fresh", "Fresh_P.utoc")) {
		t.Fatalf("%v", fxNames(ld))
	}
	if readS(t, join(ld, "Pack", "Skin", "Skin_P.utoc")) != "old" {
		t.Fatal("the file in the pack was not put back")
	}
	// a finished update leaves no marker behind
	src := join(t.TempDir(), "Hornet")
	container(t, src, "Hornet_P")
	if _, err := addFromPaths(g, []string{src}, nil); err != nil {
		t.Fatal(err)
	}
	for _, n := range fxNames(ld) {
		if strings.HasSuffix(n, incompleteSuffix) || strings.HasSuffix(n, updatingSuffix) {
			t.Fatalf("left behind: %s", n)
		}
	}
}

// inst-19: zip names that are not UTF-8 (Japanese Windows zip tools) stay apart
// and stay valid names.
func TestFixZipNamesNotUTF8(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range []string{"\x90\xC2/Blue_P.utoc", "\x90\xC2/Blue_P.ucas", "\x90\xD4/Red_P.utoc", "\x90\xD4/Red_P.ucas", "\x83\x5C/So_P.utoc"} {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: n, NonUTF8: true})
		w.Write([]byte("x"))
	}
	zw.Close()
	z := join(t.TempDir(), "skins.zip")
	write(t, z, buf.String())
	files, closer, err := readZip(z)
	if err != nil {
		t.Fatal(err)
	}
	defer closer()
	dirs := map[string]bool{}
	for _, f := range files {
		if !utf8.ValidString(f.rel) {
			t.Fatalf("%q", f.rel)
		}
		segs, ok := cleanSegments(f.rel)
		if !ok || len(segs) != 2 {
			t.Fatalf("%q -> %v %v", f.rel, segs, ok) // "\x83\x5C" (ソ) must not be split at its "\"
		}
		dirs[segs[0]] = true
	}
	if len(dirs) != 3 {
		t.Fatalf("folders %v", dirs)
	}
	g := newGame(t)
	installModding(t, g)
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil || len(res.Added) != 3 {
		t.Fatalf("%+v %v", res, err)
	}
}

// inst-20: a Skin Studio skin in a pack doesn't stop the rest of the pack.
func TestFixSkinStudioSkinInAPack(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	z := join(t.TempDir(), "Squadron pack.zip")
	makeZip(t, z, map[string]string{
		"HUD/HUD_P.utoc": "t", "HUD/HUD_P.ucas": "c",
		"Cam/Scripts/main.lua": "print(1)",
		"Skin/skinstudio.json": "{}", "Skin/Skin_P.utoc": "t", "Skin/Skin_P.ucas": "c",
	})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil || res.SkinStudio {
		t.Fatalf("%+v %v", res, err)
	}
	sort.Strings(res.Added)
	if !reflect.DeepEqual(res.Added, []string{"Cam (script mod)", "HUD"}) || !strings.Contains(strings.Join(res.Notes, " "), "Skin Studio") {
		t.Fatalf("%+v", res)
	}
	if exists(join(g.loaderDir(), "Skin")) {
		t.Fatal("the skin was added")
	}
}

// inst-21: a damaged mod-dates.json is kept aside, and one that can't be read
// right now is not overwritten.
func TestFixModDatesDamagedOrUnreadable(t *testing.T) {
	newGame(t)
	os.WriteFile(modDatesPath(), make([]byte, 40), 0o644) // zero-filled after a power cut
	recordModDates(map[string]bool{"pak:c": false}, nil)
	if b, err := os.ReadFile(modDatesPath() + ".bad"); err != nil || len(b) != 40 {
		t.Fatalf("damaged file not kept: %v", err)
	}
	if _, ok := loadModDates()["pak:c"]; !ok {
		t.Fatal("new dates not saved")
	}
	// can't be read (here: something in the way that is not a file)
	newGame(t)
	os.MkdirAll(join(modDatesPath(), "x"), 0o755)
	recordModDates(map[string]bool{"pak:d": false}, nil)
	if !isDir(modDatesPath()) || exists(modDatesPath()+".tmp") {
		t.Fatal("overwritten")
	}
}
