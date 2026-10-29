package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
)

// ---- helpers --------------------------------------------------------------------

func write(t *testing.T, p string, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func container(t *testing.T, dir, base string) {
	write(t, join(dir, base+".utoc"), "toc")
	write(t, join(dir, base+".ucas"), "cas")
	write(t, join(dir, base+".pak"), "pak")
}

func utf16le(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return string(b)
}

// a fake game install with UE4SS and the Mod Loader
func newGame(t *testing.T) Game {
	t.Helper()
	base := t.TempDir()
	dataDir = join(base, "data")
	os.MkdirAll(dataDir, 0o755)
	exeDir = join(base, "app")
	os.Setenv("ACE8_FAKE_RUNNING", join(base, "running.txt"))
	os.Unsetenv("ACE8_FAKE_STEAM")
	os.Unsetenv("ACE8_FAKE_NO_STEAM")
	settings = Settings{}
	g := Game{Root: join(base, "steamapps", "common", "ACE COMBAT 8")}
	write(t, g.GameExe(), "exe")
	write(t, g.Bootstrapper(), "eac")
	write(t, join(g.EACDir(), "Settings.json"), "{}")
	write(t, join(g.Paks(), "pakchunk0-Windows.utoc"), "game")
	settings.GameDir = g.Root
	return g
}

func installModding(t *testing.T, g Game) {
	write(t, join(g.Win64(), "dwmapi.dll"), "dwm")
	write(t, join(g.Win64(), "ue4ss", "UE4SS.dll"), "ue4ss")
	write(t, join(g.Win64(), "ue4ss", "UE4SS-settings.ini"), "[x]")
	write(t, join(g.Win64(), "ue4ss", "Mods", "mods.txt"), "IoStoreLoaderMod : 1\r\n\r\n; Built-in keybinds, do not move up!\r\nKeybinds : 1\r\n")
	ld := join(g.Win64(), "ue4ss", "Mods", "IoStoreLoaderMod")
	write(t, join(ld, "dlls", "main.dll"), "MZ..."+utf16le("1.1.0-ac8-1.1.2.0-ue5.4")+"...")
	write(t, join(ld, "enabled.txt"), "")
}

func setRunning(t *testing.T, names ...string) {
	write(t, os.Getenv("ACE8_FAKE_RUNNING"), strings.Join(names, "\n"))
}

func keys(mods []*PakMod) []string {
	var out []string
	for _, m := range mods {
		out = append(out, m.Key)
	}
	return out
}

func treeSnapshot(t *testing.T, root string) []string {
	var out []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if info.IsDir() {
			out = append(out, rel+"/")
		} else {
			b, _ := os.ReadFile(p)
			out = append(out, rel+"="+string(b))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func makeZip(t *testing.T, p string, files map[string]string) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		w, _ := zw.Create(n)
		w.Write([]byte(files[n]))
	}
	zw.Close()
	write(t, p, buf.String())
}

// ---- discovery: the same rules as the Mod Loader -----------------------------------

func discoveryTree(t *testing.T, g Game) {
	installModding(t, g)
	ld := g.loaderDir()
	container(t, join(ld, "Guided TLS"), "GTLS_SU572")
	container(t, join(ld, "SkyEdit"), "SkyEdit")
	container(t, join(ld, "SkyEditor", "packed"), "SkyEdit") // tool output: not a mod
	container(t, join(ld, "SkyEditor", "build", "Live"), "x")
	container(t, join(ld, "Su57Navy"), "Su57Navy_P")
	container(t, join(ld, "My Skin v2", "My Skin v2"), "MySkin_P")
	container(t, join(ld, "Wrapped", "old"), "w_old")
	container(t, join(ld, "Wrapped", "Inner"), "w")
	write(t, join(ld, "NoCas", "n.utoc"), "toc")
	container(t, join(ld, "Deep", "1", "2", "3", "4", "5"), "ok")
	container(t, join(ld, "Deeper", "1", "2", "3", "4", "5", "6"), "no")
	container(t, ld, "loose")
	tm := join(g.Paks(), "~mods")
	container(t, tm, "Loose_P")
	container(t, join(tm, "Old Download"), "Su57Navy_P")
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(join(tm, "Old Download", "Su57Navy_P.utoc"), old, old)
}

func TestDiscoveryMatchesLoader(t *testing.T) {
	g := newGame(t)
	discoveryTree(t, g)
	mods, stale := scanPakMods(g.loaderDir(), g.tildeModsDir())
	want := []string{"Deep", "Guided TLS", "loose.utoc", "My Skin v2", "NoCas", "SkyEdit", "Su57Navy", "Wrapped", `~mods\Loose_P.utoc`, `~mods\Old Download`}
	if got := keys(mods); !reflect.DeepEqual(got, want) {
		t.Fatalf("mods\n got %q\nwant %q", got, want)
	}
	if len(stale) != 0 {
		t.Fatalf("stale %v", stale)
	}
	var plan []string
	for _, m := range mods {
		for _, c := range m.Containers {
			if c.Skip == "" {
				rel, _ := filepath.Rel(g.Root, c.Path)
				plan = append(plan, filepath.ToSlash(rel))
			}
		}
	}
	t.Logf("plan: %q", plan)
	for _, m := range mods {
		switch m.Key {
		case "NoCas":
			if m.Loads != 0 || !strings.Contains(m.Containers[0].Skip, "n.ucas is missing") {
				t.Errorf("NoCas: %+v", m.Containers[0])
			}
		case `~mods\Old Download`:
			if m.Loads != 0 || !strings.Contains(m.Containers[0].Skip, "Su57Navy has a newer copy") {
				t.Errorf("old download: %q", m.Containers[0].Skip)
			}
		case "Wrapped":
			if len(m.Containers) != 1 || m.Containers[0].Name != "w.utoc" {
				t.Errorf("Wrapped containers %+v", m.Containers)
			}
		case "Deep":
			if len(m.Containers) != 1 {
				t.Errorf("Deep: %d", len(m.Containers))
			}
		}
	}

	// the real Mod Loader (built for Linux from its own source by its self-test) must agree
	bin := os.Getenv("ACE8_LOADER_SELFTEST")
	if bin == "" {
		t.Skip("ACE8_LOADER_SELFTEST not set")
	}
	out, err := exec.Command(bin, "--dry", g.loaderDir(), g.Paks()).CombinedOutput()
	if err != nil {
		t.Fatalf("loader dry run: %v\n%s", err, out)
	}
	var loaderPlan []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "mounting [") {
			p := strings.TrimRight(l[strings.Index(l, "path=")+5:], "\r")
			p = strings.ReplaceAll(p, `\`, "/")
			rel, _ := filepath.Rel(g.Root, filepath.Clean(p))
			loaderPlan = append(loaderPlan, filepath.ToSlash(rel))
		}
	}
	if !reflect.DeepEqual(plan, loaderPlan) {
		t.Fatalf("launcher and loader disagree\nlauncher %q\nloader   %q", plan, loaderPlan)
	}
}

func TestLoadOrder(t *testing.T) {
	g := newGame(t)
	discoveryTree(t, g)
	write(t, join(g.loaderDir(), "loadorder.txt"), "\ufeff# c\r\n+~mods/Loose_P.utoc\r\n-su57navy\r\n+Ghost\r\n  +  SkyEdit  \r\n+SkyEdit\r\n")
	mods, stale := scanPakMods(g.loaderDir(), g.tildeModsDir())
	got := keys(mods)
	if got[0] != `~mods\Loose_P.utoc` || got[1] != "Su57Navy" || got[2] != "SkyEdit" {
		t.Fatalf("order %q", got)
	}
	if mods[1].Enabled || !mods[0].Enabled {
		t.Fatal("on/off wrong")
	}
	if !reflect.DeepEqual(stale, []string{"Ghost"}) {
		t.Fatalf("stale %v", stale)
	}
	// Su57Navy is off, so the ~mods copy is no longer an "older duplicate"
	for _, m := range mods {
		if m.Key == `~mods\Old Download` && m.Loads != 1 {
			t.Fatalf("old download should load now: %+v", m.Containers[0])
		}
	}
	// saving keeps Skin Studio's list last
	e := keepSkinListLast([]orderEntry{{skinListName, true}, {"A", true}, {"B", false}})
	if e[2].Key != skinListName {
		t.Fatalf("%v", e)
	}
	if err := writeLoadOrder(g.loaderDir(), e); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(join(g.loaderDir(), "loadorder.txt"))
	if !strings.Contains(string(b), "+A\r\n-B\r\n+"+skinListName+"\r\n") {
		t.Fatalf("%q", b)
	}
}

// ---- the switch ---------------------------------------------------------------

func TestSwitchRoundTrip(t *testing.T) {
	g := newGame(t)
	discoveryTree(t, g)
	container(t, join(g.Paks(), "LogicMods"), "BPMod")
	before := treeSnapshot(t, g.Root)
	if m := g.modeInfo().Mode; m != modeModded {
		t.Fatalf("mode %s", m)
	}
	res, err := g.switchTo(modeUnmodded)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != modeUnmodded || len(res.Moved) != 4 {
		t.Fatalf("%+v", res)
	}
	for _, p := range []string{join(g.Win64(), "dwmapi.dll"), join(g.Win64(), "ue4ss"), join(g.Paks(), "~mods"), join(g.Paks(), "LogicMods")} {
		if exists(p) {
			t.Fatalf("%s still in the game", p)
		}
	}
	if !isFile(g.GameExe()) || !isFile(join(g.Paks(), "pakchunk0-Windows.utoc")) {
		t.Fatal("game files were moved")
	}
	// the mod list still works while switched off
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	if len(mods) != 10 {
		t.Fatalf("switched off: %d mods", len(mods))
	}
	if !isFile(join(g.Storage(), "README - what is this folder.txt")) {
		t.Fatal("no readme in storage")
	}
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	if after := treeSnapshot(t, g.Root); !reflect.DeepEqual(before, after) {
		t.Fatalf("game folder differs after a round trip")
	}
	if ents, _ := os.ReadDir(g.OffDir()); len(ents) != 0 {
		t.Fatalf("left over in Switched off: %v", ents)
	}
}

func TestSwitchRollback(t *testing.T) {
	g := newGame(t)
	discoveryTree(t, g)
	before := treeSnapshot(t, g.Root)
	calls := 0
	rename = func(a, b string) error {
		calls++
		if calls == 3 {
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("There is not enough space on the disk.")}
		}
		return os.Rename(a, b)
	}
	defer func() { rename = os.Rename }()
	_, err := g.switchTo(modeUnmodded)
	if err == nil || !strings.Contains(err.Error(), "Nothing was changed") || !strings.Contains(err.Error(), "The drive is full") {
		t.Fatalf("err %v", err)
	}
	if after := treeSnapshot(t, g.Root); !reflect.DeepEqual(before, after) {
		t.Fatal("not rolled back")
	}
	if m := g.modeInfo().Mode; m != modeModded {
		t.Fatalf("mode %s", m)
	}
	// a clean rollback leaves nothing to finish
	if s := buildState(); s.UnfinishedSw != "" {
		t.Fatalf("unfinished switch reported after a clean rollback: %q", s.UnfinishedSw)
	}
}

func TestSwitchAcrossDrives(t *testing.T) {
	g := newGame(t)
	discoveryTree(t, g)
	old := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	f := join(g.loaderDir(), "Su57Navy", "Su57Navy_P.utoc")
	os.Chtimes(f, old, old)
	rename = func(a, b string) error {
		if strings.Contains(b, "mod storage") && !strings.Contains(a, "mod storage") {
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: syscall.EXDEV}
		}
		return os.Rename(a, b)
	}
	defer func() { rename = os.Rename }()
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	if m := g.modeInfo().Mode; m != modeUnmodded {
		t.Fatalf("mode %s", m)
	}
	moved := join(g.OffDir(), "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "Su57Navy", "Su57Navy_P.utoc")
	st, err := os.Stat(moved)
	if err != nil || !st.ModTime().Equal(old) {
		t.Fatalf("copy lost its date: %v %v", err, st)
	}
	ents, _ := os.ReadDir(g.Win64())
	for _, e := range ents {
		if strings.Contains(e.Name(), "older copy") {
			t.Fatalf("left behind in the game: %s", e.Name())
		}
	}
}

// A program holding a folder open (the way an app watching ue4ss\Mods does):
// Windows won't move that folder or any folder above it, but will move what is inside it.
func holdOpen(dirs ...string) func() {
	rename = func(a, b string) error {
		for _, d := range dirs {
			if samePath(a, d) || isInside(d, a) {
				return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("Access is denied.")}
			}
		}
		return os.Rename(a, b)
	}
	return func() { rename = os.Rename }
}

func TestSwitchWithAFolderHeldOpen(t *testing.T) {
	g := newGame(t)
	discoveryTree(t, g)
	beforeMods := treeSnapshot(t, join(g.Win64(), "ue4ss"))
	game := join(g.Win64(), "ue4ss", "Mods")
	parked := join(g.OffDir(), "Game", "Binaries", "Win64", "ue4ss", "Mods")
	release := holdOpen(game, parked)
	defer release()

	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	if m := g.modeInfo().Mode; m != modeUnmodded {
		t.Fatalf("mode %s", m)
	}
	if hasFiles(join(g.Win64(), "ue4ss")) || exists(join(g.Win64(), "dwmapi.dll")) || hasFiles(join(g.Paks(), "~mods")) {
		t.Fatal("files left in the game")
	}
	if !isFile(join(parked, "IoStoreLoaderMod", "dlls", "main.dll")) {
		t.Fatal("the Mod Loader was not switched off")
	}
	if g.loaderDir() != join(parked, "IoStoreLoaderMod") {
		t.Fatalf("loader dir %s", g.loaderDir())
	}
	if s := buildState(); s.UnfinishedSw != "" || s.Mode.Mode != modeUnmodded {
		t.Fatalf("state %q %q", s.UnfinishedSw, s.Mode.Mode)
	}
	// and back, with the folders still held open on both sides
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	if m := g.modeInfo().Mode; m != modeModded {
		t.Fatalf("mode %s", m)
	}
	if after := treeSnapshot(t, join(g.Win64(), "ue4ss")); !reflect.DeepEqual(beforeMods, after) {
		t.Fatalf("ue4ss changed:\n%v\n%v", beforeMods, after)
	}
	release()
	if _, err := g.switchTo(modeUnmodded); err != nil { // nothing held: whole folders again
		t.Fatal(err)
	}
	if exists(join(g.Win64(), "ue4ss")) {
		t.Fatal("empty ue4ss folder left in the game")
	}
}

func TestSwitchWithAFileHeldOpen(t *testing.T) {
	g := newGame(t)
	discoveryTree(t, g)
	before := treeSnapshot(t, g.Root)
	dll := join(g.loaderDir(), "dlls", "main.dll")
	release := holdOpen(dll)
	defer release()
	_, err := g.switchTo(modeUnmodded)
	if err == nil || !strings.Contains(err.Error(), "main.dll") || !strings.Contains(err.Error(), "Nothing was changed") {
		t.Fatalf("err %v", err)
	}
	release()
	var after []string
	for _, e := range treeSnapshot(t, g.Root) {
		after = append(after, e)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("not put back:\n%v\n%v", before, after)
	}
	if s := buildState(); s.UnfinishedSw != "" || s.Mode.Mode != modeModded {
		t.Fatalf("state %q %q", s.UnfinishedSw, s.Mode.Mode)
	}
}

func TestSwitchKeepsCopyInTheWay(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	// the player installs UE4SS again by hand while switched off
	write(t, join(g.Win64(), "dwmapi.dll"), "dwm-new")
	if m := g.modeInfo().Mode; m != modeMixed {
		t.Fatalf("mode %s", m)
	}
	res, err := g.switchTo(modeModded)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Aside) != 1 || !strings.Contains(res.Aside[0], "dwmapi (older copy ") || !strings.HasSuffix(res.Aside[0], ".dll") {
		t.Fatalf("aside %v", res.Aside)
	}
	b, _ := os.ReadFile(res.Aside[0])
	if string(b) != "dwm-new" {
		t.Fatal("wrong copy set aside")
	}
	b, _ = os.ReadFile(join(g.Win64(), "dwmapi.dll"))
	if string(b) != "dwm" {
		t.Fatal("wrong copy in the game")
	}
}

func TestSwitchRefusesWhileGameRuns(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	setRunning(t, "AceCombat8.exe")
	if _, err := g.switchTo(modeUnmodded); err != errGameRunning {
		t.Fatalf("err %v", err)
	}
	if g.modeInfo().Mode != modeModded {
		t.Fatal("moved anyway")
	}
}

// ---- adding mods -------------------------------------------------------------------

func TestAddZipWithVersions(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	z := join(t.TempDir(), "Su-57 Liveries v3.zip")
	makeZip(t, z, map[string]string{
		"Su-57 Liveries/Blue/Su57Livery_P.utoc": "toc", "Su-57 Liveries/Blue/Su57Livery_P.ucas": "blue", "Su-57 Liveries/Blue/Su57Livery_P.pak": "p",
		"Su-57 Liveries/Red/Su57Livery_P.utoc": "toc", "Su-57 Liveries/Red/Su57Livery_P.ucas": "red", "Su-57 Liveries/Red/Su57Livery_P.pak": "p",
		"Su-57 Liveries/readme.txt": "hi", "__MACOSX/._x": "junk",
	})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Choices) != 2 || res.Choices[0].Name != "Blue" || len(res.Added) != 0 {
		t.Fatalf("%+v", res)
	}
	res, err = addFromPaths(g, []string{z}, []string{res.Choices[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Added, []string{"Red"}) {
		t.Fatalf("%+v", res)
	}
	b, _ := os.ReadFile(join(g.loaderDir(), "Red", "Su57Livery_P.ucas"))
	if string(b) != "red" {
		t.Fatal("wrong files")
	}
	// adding the other one later replaces Red: it is the same mod in another colour
	res, err = addFromPaths(g, []string{z}, []string{"pak:blue"})
	if err != nil {
		t.Fatal(err)
	}
	if exists(join(g.loaderDir(), "Red")) || !exists(join(g.loaderDir(), "Blue")) {
		t.Fatal("Red should have been replaced")
	}
	if len(g.listRemoved()) != 1 || !strings.Contains(strings.Join(res.Notes, " "), "moved to Removed mods") {
		t.Fatalf("removed %+v notes %v", g.listRemoved(), res.Notes)
	}
}

func TestAddUE4SSLayoutZip(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	z := join(t.TempDir(), "Su57_GTLS_v1.0.zip")
	makeZip(t, z, map[string]string{
		"ue4ss/Mods/Su57GTLS/Scripts/main.lua":                   "-- Su-57 guided laser\nprint(1)",
		"ue4ss/Mods/Su57GTLS/Scripts/RoGText.dll":                "dll",
		"ue4ss/Mods/Su57GTLS/enabled.txt":                        "",
		"ue4ss/Mods/IoStoreLoaderMod/Guided TLS/GTLS_SU572.utoc": "toc",
		"ue4ss/Mods/IoStoreLoaderMod/Guided TLS/GTLS_SU572.ucas": "cas",
		"ue4ss/Mods/IoStoreLoaderMod/dlls/main.dll":              "OLD LOADER",
		"ue4ss/Mods/mods.txt":                                    "Su57GTLS : 1",
		"README.txt":                                             "readme",
	})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(res.Added)
	if !reflect.DeepEqual(res.Added, []string{"Guided TLS", "Su57GTLS (script mod)"}) {
		t.Fatalf("%+v", res)
	}
	if b, _ := os.ReadFile(join(g.loaderDir(), "dlls", "main.dll")); strings.Contains(string(b), "OLD") {
		t.Fatal("the loader was replaced")
	}
	if !strings.Contains(strings.Join(res.Notes, " "), "own copy of the Mod Loader") {
		t.Fatalf("notes %v", res.Notes)
	}
	sm := listScriptMods(g.ue4ssRoot())
	if len(sm) != 1 || !sm[0].Enabled || sm[0].Description != "Su-57 guided laser" {
		t.Fatalf("%+v", sm)
	}
	if b, _ := os.ReadFile(join(g.ue4ssRoot(), "Mods", "mods.txt")); strings.Contains(string(b), "Su57GTLS") {
		t.Fatal("mods.txt was replaced by the download's")
	}
}

func TestAddLooseFilesAndScriptFolder(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	src := t.TempDir()
	container(t, src, "Hornet_P")
	res, err := addFromPaths(g, []string{join(src, "Hornet_P.utoc")}, nil) // just the .utoc picked: .ucas and .pak come along
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Added, []string{"Hornet_P"}) || !isFile(join(g.loaderDir(), "Hornet_P", "Hornet_P.ucas")) {
		t.Fatalf("%+v", res)
	}
	sf := join(t.TempDir(), "CoolCam")
	write(t, join(sf, "Scripts", "main.lua"), "print('cam')")
	res, err = addFromPaths(g, []string{sf}, nil)
	if err != nil || !reflect.DeepEqual(res.Added, []string{"CoolCam (script mod)"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if !isFile(join(g.ue4ssRoot(), "Mods", "CoolCam", "enabled.txt")) {
		t.Fatal("not switched on")
	}
}

func TestAddRefusesFilesAlreadyInTheGame(t *testing.T) {
	g := newGame(t)
	discoveryTree(t, g)
	before := treeSnapshot(t, g.Root)
	for _, p := range []string{g.loaderDir(), join(g.loaderDir(), "Alpha"), filepath.Dir(g.Root), join(g.Paks(), "~mods")} {
		_, err := addFromPaths(g, []string{p}, nil)
		if err == nil || !strings.Contains(err.Error(), "can't be added from there") {
			t.Fatalf("%s: err %v", p, err)
		}
	}
	if after := treeSnapshot(t, g.Root); !reflect.DeepEqual(before, after) {
		t.Fatal("the game changed")
	}
	// a copy of the Mod Loader's folder from somewhere else is not a script mod; its mods still come in
	src := join(t.TempDir(), "IoStoreLoaderMod")
	write(t, join(src, "dlls", "main.dll"), "MZ")
	write(t, join(src, "enabled.txt"), "")
	container(t, join(src, "Zeta"), "Zeta_P")
	res, err := addFromPaths(g, []string{src}, nil)
	if err != nil || !reflect.DeepEqual(res.Added, []string{"Zeta"}) {
		t.Fatalf("%+v %v", res, err)
	}
	b, _ := os.ReadFile(join(g.loaderDir(), "dlls", "main.dll"))
	if !strings.Contains(string(b), utf16le("1.1.0")) {
		t.Fatal("the Mod Loader was replaced")
	}
}

func TestAddWhileUnmoddedLeavesTheGameAlone(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, g.Root)
	z := join(t.TempDir(), "pack.zip")
	makeZip(t, z, map[string]string{"Paks/~mods/Loose_P.utoc": "t", "Paks/~mods/Loose_P.ucas": "c", "Paks/LogicMods/Thing.pak": "p", "Folder/Folder_P.utoc": "t", "Folder/Folder_P.ucas": "c"})
	if _, err := addFromPaths(g, []string{z}, nil); err != nil {
		t.Fatal(err)
	}
	if after := treeSnapshot(t, g.Root); !reflect.DeepEqual(before, after) {
		t.Fatal("adding while Unmodded changed the game")
	}
	if m := g.modeInfo().Mode; m != modeUnmodded {
		t.Fatalf("mode %s", m)
	}
	off := join(g.OffDir(), "Game")
	for _, f := range []string{join(off, "Content", "Paks", "~mods", "Loose_P.utoc"), join(off, "Content", "Paks", "LogicMods", "Thing.pak"),
		join(off, "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "Folder", "Folder_P.ucas")} {
		if !isFile(f) {
			t.Fatalf("missing %s", f)
		}
	}
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	if !isFile(join(g.Paks(), "~mods", "Loose_P.utoc")) || !isFile(join(g.Paks(), "LogicMods", "Thing.pak")) {
		t.Fatal("not in the game after switching to Modded")
	}
}

func TestAddLooseFileKeepsOlderCopy(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	tm := join(g.Paks(), "~mods")
	write(t, join(tm, "Loose_P.utoc"), "old-toc")
	write(t, join(tm, "Loose_P.ucas"), "old-cas")
	write(t, join(tm, "Loose_P.sig"), "old-sig")
	z := join(t.TempDir(), "new.zip")
	makeZip(t, z, map[string]string{"~mods/Loose_P.utoc": "new-toc", "~mods/Loose_P.ucas": "new-cas"})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil || len(res.Updated) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	b, _ := os.ReadFile(join(tm, "Loose_P.utoc"))
	if string(b) != "new-toc" || exists(join(tm, "Loose_P.sig")) {
		t.Fatal("new copy not in place, or the old .sig was left with it")
	}
	rm := g.listRemoved()
	if len(rm) != 1 || rm[0].Kind != "tilde" {
		t.Fatalf("removed %+v", rm)
	}
	dir, _, _ := g.removedEntry(rm[0].ID)
	for _, f := range []string{"Loose_P.utoc", "Loose_P.ucas", "Loose_P.sig"} {
		if b, _ := os.ReadFile(join(dir, f)); !strings.HasPrefix(string(b), "old-") {
			t.Fatalf("older %s not kept", f)
		}
	}
}

func TestAddPutsOldCopyBackWhenTheNewOneCantGoIn(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	container(t, join(g.loaderDir(), "Hornet"), "Hornet_P")
	before := treeSnapshot(t, g.Root)
	rename = func(a, b string) error {
		if strings.Contains(a, "Adding") {
			return &os.LinkError{Op: "rename", Old: a, New: b, Err: errors.New("Access is denied.")}
		}
		return os.Rename(a, b)
	}
	defer func() { rename = os.Rename }()
	src := join(t.TempDir(), "Hornet")
	write(t, join(src, "Hornet_P.utoc"), "new")
	write(t, join(src, "Hornet_P.ucas"), "new")
	_, err := addFromPaths(g, []string{src}, nil)
	if err == nil || !strings.Contains(err.Error(), "Nothing was changed") {
		t.Fatalf("err %v", err)
	}
	var after []string
	for _, e := range treeSnapshot(t, g.Root) {
		if !strings.Contains(e, "loadorder.txt") { // putting it back tidies the load order: fine
			after = append(after, e)
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("the old copy was not put back:\n%v\n%v", before, after)
	}
	if rm := g.listRemoved(); len(rm) != 0 {
		t.Fatalf("removed list %+v", rm)
	}
	if exists(join(g.Storage(), "Adding")) {
		t.Fatal("staging folder left behind")
	}
}

func TestAddScriptUpdateKeepsItOff(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	sf := join(t.TempDir(), "CoolCam")
	write(t, join(sf, "Scripts", "main.lua"), "print('v1')")
	if _, err := addFromPaths(g, []string{sf}, nil); err != nil {
		t.Fatal(err)
	}
	if err := setScriptMod(g.ue4ssRoot(), "CoolCam", false); err != nil {
		t.Fatal(err)
	}
	write(t, join(sf, "Scripts", "main.lua"), "print('v2')")
	res, err := addFromPaths(g, []string{sf}, nil)
	if err != nil || len(res.Updated) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, m := range listScriptMods(g.ue4ssRoot()) {
		if m.Name == "CoolCam" && m.Enabled {
			t.Fatal("the update switched it back on")
		}
	}
}

func TestCleanSegments(t *testing.T) {
	for _, bad := range []string{"../x.utoc", "a/../../x", "C:/x.utoc", "a/CON/x.utoc", "a/nul.txt", "a/b:c/x.utoc", "__MACOSX/a/x", "a/._x.utoc", "..."} {
		if s, ok := cleanSegments(bad); ok {
			t.Errorf("%q accepted as %v", bad, s)
		}
	}
	if s, ok := cleanSegments(`Mod v1. /Mod_P.utoc`); !ok || s[0] != "Mod v1" {
		t.Errorf("trailing dot/space: %v %v", s, ok)
	}
}

func TestAddSkinStudioSkinAndJunk(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	z := join(t.TempDir(), "skin.zip")
	makeZip(t, z, map[string]string{"navy/skinstudio.json": "{}", "navy/navy_P.utoc": "t", "navy/navy_P.ucas": "c"})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil || !res.SkinStudio || exists(join(g.loaderDir(), "navy")) {
		t.Fatalf("%+v %v", res, err)
	}
	z2 := join(t.TempDir(), "notamod.zip")
	makeZip(t, z2, map[string]string{"readme.txt": "x"})
	if _, err := addFromPaths(g, []string{z2}, nil); err == nil || !strings.Contains(err.Error(), "no mod was found") {
		t.Fatalf("err %v", err)
	}
	r7 := join(t.TempDir(), "mod.7z")
	write(t, r7, "x")
	if _, err := addFromPaths(g, []string{r7}, nil); err == nil || !strings.Contains(err.Error(), "Unpack it first") {
		t.Fatalf("err %v", err)
	}
	z3 := join(t.TempDir(), "evil.zip")
	makeZip(t, z3, map[string]string{"../../escape/x_P.utoc": "t", "ok/x_P.utoc": "t", "ok/x_P.ucas": "c"})
	res, err = addFromPaths(g, []string{z3}, nil)
	if err != nil || exists(join(filepath.Dir(g.Root), "escape")) {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRemoveAndRestore(t *testing.T) {
	g := newGame(t)
	discoveryTree(t, g)
	write(t, join(g.ue4ssRoot(), "Mods", "RoGLaser", "Scripts", "main.lua"), "x")
	before := treeSnapshot(t, g.Root)
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	for _, m := range mods {
		if m.Key == "loose.utoc" || m.Key == "Su57Navy" || m.Key == `~mods\Loose_P.utoc` {
			if _, err := g.removeToStorage(m.Where, m.Key, m.Path, m.IsFile); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := g.removeToStorage("script", "RoGLaser", join(g.ue4ssRoot(), "Mods", "RoGLaser"), false); err != nil {
		t.Fatal(err)
	}
	if exists(join(g.loaderDir(), "loose.ucas")) || exists(join(g.loaderDir(), "Su57Navy")) {
		t.Fatal("not removed")
	}
	rm := g.listRemoved()
	if len(rm) != 4 {
		t.Fatalf("%+v", rm)
	}
	// switch off in between: restoring must follow the folders
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	for _, r := range rm {
		if err := g.restoreRemoved(r.ID); err != nil {
			t.Fatalf("restore %s: %v", r.Name, err)
		}
	}
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	after := treeSnapshot(t, g.Root)
	strip := func(xs []string) []string {
		var o []string
		for _, x := range xs {
			if !strings.Contains(x, "loadorder.txt") {
				o = append(o, x)
			}
		}
		return o
	}
	if !reflect.DeepEqual(strip(before), strip(after)) {
		t.Fatalf("not restored")
	}
	if len(g.listRemoved()) != 0 {
		t.Fatal("removed list not empty")
	}
}

// ---- script mods ---------------------------------------------------------------------

func TestScriptMods(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	md := join(g.ue4ssRoot(), "Mods")
	write(t, join(md, "SkyLive", "Scripts", "main.lua"), "-- SkyLive 1.1: live sky\n")
	write(t, join(md, "SkyLive", "enabled.off"), "")
	write(t, join(md, "RoGLaser", "Scripts", "main.lua"), "local x = 1\n")
	write(t, join(md, "RoGLaser", "enabled.txt"), "")
	write(t, join(md, "CppThing", "dlls", "main.dll"), "x")
	write(t, join(md, "Listed", "Scripts", "main.lua"), "")
	write(t, join(md, "mods.txt"), "IoStoreLoaderMod : 1\r\nListed : 0\r\n; Built-in keybinds, do not move up!\r\nKeybinds : 1\r\n")
	write(t, join(md, "mods.json"), `[{"mod_name":"RoGLaser","mod_enabled":true}]`)
	sm := listScriptMods(g.ue4ssRoot())
	state := map[string]bool{}
	for _, m := range sm {
		state[m.Name] = m.Enabled
	}
	if !reflect.DeepEqual(state, map[string]bool{"CppThing": false, "Listed": false, "RoGLaser": true, "SkyLive": false}) {
		t.Fatalf("%v", state)
	}
	if sm[3].Description != "SkyLive 1.1: live sky" || sm[2].Description != "" {
		t.Fatalf("%+v %+v", sm[3], sm[2])
	}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(setScriptMod(g.ue4ssRoot(), "SkyLive", true))
	must(setScriptMod(g.ue4ssRoot(), "RoGLaser", false))
	must(setScriptMod(g.ue4ssRoot(), "Listed", true))
	if !isFile(join(md, "SkyLive", "enabled.txt")) || exists(join(md, "SkyLive", "enabled.off")) {
		t.Fatal("SkyLive")
	}
	if exists(join(md, "RoGLaser", "enabled.txt")) || !isFile(join(md, "RoGLaser", "enabled.off")) {
		t.Fatal("RoGLaser")
	}
	b, _ := os.ReadFile(join(md, "mods.txt"))
	if !strings.Contains(string(b), "Listed : 1\r\n") || !strings.Contains(string(b), "Keybinds : 1") {
		t.Fatalf("%q", b)
	}
	j, _ := os.ReadFile(join(md, "mods.json"))
	if !strings.Contains(string(j), `"mod_enabled":false`) {
		t.Fatalf("%s", j)
	}
	for _, m := range listScriptMods(g.ue4ssRoot()) {
		if m.Enabled != (m.Name != "RoGLaser" && m.Name != "CppThing") {
			t.Fatalf("%s %v", m.Name, m.Enabled)
		}
	}
}

// ---- setup -----------------------------------------------------------------------

// the Mod Loader download, unzipped next to the launcher's folder
func bundle(t *testing.T, loaderVer string) {
	bd := join(filepath.Dir(exeDir), "War’s Mod Loader "+loaderVer)
	bundleAt = time.Time{}
	defer func() { bundleAt = time.Time{} }()
	write(t, join(bd, "Game", "Binaries", "Win64", "dwmapi.dll"), "dwm-bundled")
	write(t, join(bd, "Game", "Binaries", "Win64", "ue4ss", "UE4SS.dll"), "ue4ss-bundled")
	write(t, join(bd, "Game", "Binaries", "Win64", "ue4ss", "UE4SS-settings.ini"), "[bundled]")
	write(t, join(bd, "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "dlls", "main.dll"), utf16le(loaderVer+"-ac8-1.1.2.0-ue5.4"))
	write(t, join(bd, "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "enabled.txt"), "")
	write(t, join(bd, "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "README.txt"), "readme "+loaderVer)
	write(t, join(bd, "EasyAntiCheat", "AC8_Offline.json"), `{"allow_null_client": "true"}`)
	write(t, join(bd, "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "ModLauncher.bat"), "@echo off")
	write(t, join(bd, "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "ModLauncher.ps1"), "#")
}

func TestSetupFreshAndUpdate(t *testing.T) {
	g := newGame(t)
	bundle(t, "1.1.0")
	st := g.setupStatus()
	if st.UE4SS || st.Loader != "" || !st.NeedsSetup || st.Bundled != "1.1.0" {
		t.Fatalf("%+v", st)
	}
	if _, err := g.setup(); err != nil {
		t.Fatal(err)
	}
	st = g.setupStatus()
	if !st.UE4SS || st.Loader != "1.1.0" || !st.LoaderOn || st.NeedsSetup {
		t.Fatalf("%+v", st)
	}
	if exists(join(g.EACDir(), "AC8_Offline.json")) || exists(join(g.Win64(), "steam_appid.txt")) || exists(join(g.loaderDir(), "ModLauncher.bat")) || exists(join(g.loaderDir(), "ModLauncher.ps1")) {
		t.Fatal("setup changed files outside Game\\Binaries\\Win64\\ue4ss")
	}

	// an older loader and the player's own settings
	g2 := newGame(t)
	bundle(t, "1.1.0")
	installModding(t, g2)
	write(t, join(g2.loaderDir(), "dlls", "main.dll"), utf16le("0.2.0-ac8-1.1.2.0-ue5.4"))
	write(t, join(g2.loaderDir(), "loadorder.txt"), "+Mine\r\n")
	write(t, join(g2.ue4ssRoot(), "Mods", "mods.txt"), "IoStoreLoaderMod : 0\r\nKeybinds : 1\r\n")
	st = g2.setupStatus()
	if st.Loader != "0.2.0" || !st.CanUpdate || st.LoaderOn {
		t.Fatalf("%+v", st)
	}
	if _, err := g2.setup(); err != nil {
		t.Fatal(err)
	}
	st = g2.setupStatus()
	if st.Loader != "1.1.0" || !st.LoaderOn || st.CanUpdate {
		t.Fatalf("%+v", st)
	}
	if b, _ := os.ReadFile(join(g2.ue4ssRoot(), "UE4SS-settings.ini")); string(b) != "[x]" {
		t.Fatal("settings replaced")
	}
	if b, _ := os.ReadFile(join(g2.Win64(), "dwmapi.dll")); string(b) != "dwm" {
		t.Fatal("dwmapi replaced")
	}
	if b, _ := os.ReadFile(join(g2.loaderDir(), "loadorder.txt")); string(b) != "+Mine\r\n" {
		t.Fatal("load order replaced")
	}
	rm := g2.listRemoved()
	if len(rm) != 1 || rm[0].Kind != "modloader" {
		t.Fatalf("old loader not kept: %+v", rm)
	}
	// unmodded: setup waits
	g2.switchTo(modeUnmodded)
	if _, err := g2.setup(); err == nil {
		t.Fatal("setup while switched off")
	}
}

// ---- playing -------------------------------------------------------------------------

func TestPlay(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	if _, err := g.play(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(join(dataDir, "started.txt"))
	s := string(b)
	if !strings.Contains(s, "Win64/AceCombat8.exe dir=") || !strings.Contains(s, `args=["-eac-nop-loaded" "-nosplash"]`) ||
		!strings.Contains(s, `env=["SteamAppId=2288340" "SteamGameId=2288340"]`) || strings.Contains(s, "start_protected_game") {
		t.Fatalf("%s", s)
	}
	if exists(join(g.EACDir(), "AC8_Offline.json")) || exists(join(g.Win64(), "steam_appid.txt")) {
		t.Fatal("playing changed files in the game")
	}
	setRunning(t, "AceCombat8.exe")
	if _, err := g.play(); err == nil {
		t.Fatal("played twice")
	}
	setRunning(t)
	g.switchTo(modeUnmodded)
	os.Remove(join(dataDir, "started.txt"))
	if _, err := g.play(); err != nil {
		t.Fatal(err)
	}
	o, _ := os.ReadFile(join(dataDir, "opened.txt"))
	if !strings.Contains(string(o), "steam://rungameid/2288340") || exists(join(dataDir, "started.txt")) {
		t.Fatalf("unmodded should go through Steam: %s", o)
	}
	g.switchTo(modeModded)
	updateSettings(func(s *Settings) { s.ExtraArgs = `-dx11 "-x y"` })
	os.Remove(join(dataDir, "started.txt"))
	g.play()
	b, _ = os.ReadFile(join(dataDir, "started.txt"))
	if !strings.Contains(string(b), `"-eac-nop-loaded" "-nosplash" "-dx11" "-x y"`) {
		t.Fatalf("extra args: %s", b)
	}
	// Steam not running: it is started first
	os.Setenv("ACE8_FAKE_NO_STEAM", "1")
	defer os.Unsetenv("ACE8_FAKE_NO_STEAM")
	os.Remove(join(dataDir, "opened.txt"))
	start := time.Now()
	g.play()
	o, _ = os.ReadFile(join(dataDir, "opened.txt"))
	if !strings.Contains(string(o), "steam://open/main") {
		t.Fatalf("Steam was not started: %s", o)
	}
	t.Logf("waited %v for Steam", time.Since(start).Round(time.Second))
	// Steam not running, and found: started in the background with -silent
	steamHome := t.TempDir()
	write(t, join(steamHome, "steam.exe"), "steam")
	os.Setenv("ACE8_FAKE_STEAM", steamHome)
	os.Remove(join(dataDir, "started.txt"))
	if _, err := g.play(); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(join(dataDir, "started.txt"))
	if !strings.Contains(string(b), `steam.exe dir=`) || !strings.Contains(string(b), `args=["-silent"]`) || !strings.Contains(string(b), "AceCombat8.exe") {
		t.Fatalf("Steam start: %s", b)
	}
	os.Unsetenv("ACE8_FAKE_STEAM")
	os.Unsetenv("ACE8_FAKE_NO_STEAM")
	setRunning(t)
	// Steam open but not signed in: Play waits a while, then says so
	steamSignInWait = time.Second
	defer func() { steamSignInWait = 90 * time.Second }()
	os.Setenv("ACE8_FAKE_SIGNED_OUT", "1")
	_, err := g.play()
	os.Unsetenv("ACE8_FAKE_SIGNED_OUT")
	if err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("signed out: %v", err)
	}
	// AceCombat8.exe set to run as administrator: started through Windows' prompt instead
	os.Setenv("ACE8_FAKE_NEEDS_ADMIN", "1")
	os.Remove(join(g.Win64(), "steam_appid.txt"))
	os.Remove(join(dataDir, "started.txt"))
	setRunning(t, "steam.exe")
	li, err := g.play()
	if err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(join(dataDir, "started.txt"))
	if !strings.Contains(string(b), "elevated exe=") || !strings.Contains(string(b), `"-eac-nop-loaded" "-nosplash"`) {
		t.Fatalf("admin start: %s", b)
	}
	if id, _ := os.ReadFile(join(g.Win64(), "steam_appid.txt")); string(id) != "2288340" {
		t.Fatalf("steam_appid.txt: %q", id)
	}
	if !strings.Contains(li.Note, "untick") {
		t.Fatalf("note: %q", li.Note)
	}
	os.Setenv("ACE8_FAKE_DECLINE", "1")
	if _, err := g.play(); err == nil || !strings.Contains(err.Error(), "not given") {
		t.Fatalf("declined: %v", err)
	}
	os.Unsetenv("ACE8_FAKE_DECLINE")
	os.Unsetenv("ACE8_FAKE_NEEDS_ADMIN")
	setRunning(t)
	// half switched: no play
	write(t, join(g.OffDir(), "Game", "Binaries", "Win64", "dwmapi.dll"), "x")
	if _, err := g.play(); err == nil || !strings.Contains(err.Error(), "half done") {
		t.Fatalf("%v", err)
	}
}

// ---- Steam -------------------------------------------------------------------------

func TestSteamDetection(t *testing.T) {
	g := newGame(t)
	steam := t.TempDir()
	lib := filepath.Dir(filepath.Dir(filepath.Dir(g.Root))) // …/steamapps/common/ACE COMBAT 8 -> library
	write(t, join(steam, "steam.exe"), "")
	write(t, join(steam, "steamapps", "libraryfolders.vdf"), `"libraryfolders"
{
	"0"
	{
		"path"		"`+strings.ReplaceAll(steam, `\`, `\\`)+`"
		"apps" { "228980" "0" }
	}
	"1"
	{
		"path"		"`+strings.ReplaceAll(lib, `\`, `\\`)+`"
	}
}`)
	write(t, join(lib, "steamapps", "appmanifest_2288340.acf"), `"AppState" { "appid" "2288340" "installdir" "ACE COMBAT 8" }`)
	write(t, join(steam, "userdata", "123", "config", "localconfig.vdf"), `"UserLocalConfigStore"
{
	"Software" { "Valve" { "Steam" { "apps" {
		"2288340"
		{
			"LaunchOptions"		"cmd /c \"set EOS_USE_ANTICHEATCLIENTNULL=1&& %command% -anticheat_settings=AC8_Offline.json\""
		}
	} } } }
}`)
	os.Setenv("ACE8_FAKE_STEAM", steam)
	defer os.Unsetenv("ACE8_FAKE_STEAM")
	if got := detectGame(); got != g.Root {
		t.Fatalf("detect %q want %q", got, g.Root)
	}
	opts := steamLaunchOptions()
	if len(opts) != 1 || !isOfflineLaunchOptions(opts[0]) || !strings.Contains(opts[0], `"set EOS`) {
		t.Fatalf("%q", opts)
	}
	for _, p := range []string{g.Root, g.Win64(), g.GameExe(), filepath.Dir(g.Root)} {
		if r, err := normalizeGameDir(p); err != nil || r != g.Root {
			t.Fatalf("normalize %s: %s %v", p, r, err)
		}
	}
	if _, err := normalizeGameDir(t.TempDir()); err == nil {
		t.Fatal("accepted a folder that is not the game")
	}
}

// ---- files from the user's PC ----------------------------------------------------------

func TestRealLoaderLog(t *testing.T) {
	b, err := os.ReadFile("testdata/AC8IoStoreLoader.log")
	if err != nil {
		t.Skip("no testdata")
	}
	ri := parseRunLog(decodeText(b))
	if ri.State != "ok" || ri.Mounted != 4 || ri.Total != 4 || ri.Version != "1.1.0" || !strings.Contains(ri.NewFiles, "4 of 4") {
		t.Fatalf("%+v", ri)
	}
	if r := ri.Mods["navy_paint"]; r == nil || r.Loaded != 1 {
		t.Fatalf("%+v", ri.Mods)
	}
	bad := parseRunLog("AC8 mod loader 1.1.0\r\nmount signature matches=0 address=0\r\npatterns not found exactly once - is this a different game version? nothing changed\r\n")
	if bad.State != "unsupported" {
		t.Fatalf("%+v", bad)
	}
	part := parseRunLog("AC8 mod loader 1.1.0\nhook installed, waiting for the game to mount pakchunk0-Windows.utoc\nmounting [A] order=1000 path=x\n  result code=5 message=Corrupt toc\nmounting [B] order=1001 path=y\n  result code=0 message=OK\ndone: 1 of 2 containers mounted from 2 mod(s)\n")
	if part.State != "problems" || part.Mods["a"].Failed != 1 || !strings.Contains(part.Mods["a"].Messages[0], "Corrupt toc") {
		t.Fatalf("%+v %+v", part, part.Mods["a"])
	}
}

func TestRealUtoc(t *testing.T) {
	toc, err := readUtoc("testdata/rodsFROMGOD24.utoc")
	if err != nil {
		t.Skip("no testdata")
	}
	names := toc.assetNames()
	if len(names) != 8 || names[0] != "Blueprints/Vehicle/PLAYER/Plane/PP0010_e18g/BP_PlayerPlane_PP0010_e18g" {
		t.Fatalf("%q", names)
	}
	a, _ := readUtoc("testdata/SkyEdit.utoc")
	b, _ := readUtoc("testdata/SkyEdit_packed.utoc")
	common := 0
	for id := range a.packageIDs() {
		if b.packageIDs()[id] {
			common++
		}
	}
	if common != 3 {
		t.Fatalf("common %d", common)
	}
	n, _ := readUtoc("testdata/navypaint_kbqow_P.utoc")
	if len(n.assetNames()) != 4 {
		t.Fatalf("%q", n.assetNames())
	}
}

func TestConflictNotes(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	ld := g.loaderDir()
	for _, m := range []struct{ dir, src string }{{"SkyA", "testdata/SkyEdit.utoc"}, {"SkyB", "testdata/SkyEdit_packed.utoc"}} {
		b, err := os.ReadFile(m.src)
		if err != nil {
			t.Skip("no testdata")
		}
		write(t, join(ld, m.dir, m.dir+".utoc"), string(b))
		write(t, join(ld, m.dir, m.dir+".ucas"), "x")
	}
	mods, _ := scanPakMods(ld, g.tildeModsDir())
	addModNotes(mods, nil, modeModded)
	if len(mods[0].Notes) != 1 || mods[0].Notes[0].Kind != "warn" || !strings.Contains(mods[0].Notes[0].Text, "3 game files are also changed by SkyB") {
		t.Fatalf("%+v", mods[0].Notes)
	}
	if len(mods[1].Notes) != 1 || mods[1].Notes[0].Kind != "info" {
		t.Fatalf("%+v", mods[1].Notes)
	}
	if len(mods[1].Containers[0].Files) != 4 {
		t.Fatalf("%q", mods[1].Containers[0].Files)
	}
}

func TestLoaderVersionReal(t *testing.T) {
	for file, want := range map[string]string{"testdata/main-1.1.0.dll": "1.1.0", "testdata/main-0.2.0.dll": "0.2.0"} {
		if _, err := os.Stat(file); err != nil {
			t.Skip("no testdata")
		}
		if got := loaderVersion(file); got != want {
			t.Errorf("%s: %q, want %q", file, got, want)
		}
	}
}

// ---- choosing the storage folder -------------------------------------------------

func TestMoveStorage(t *testing.T) {
	g := newGame(t)
	discoveryTree(t, g)
	before := treeSnapshot(t, g.Root)
	// a mod removed while Unmodded remembers a path inside the storage folder
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	if _, err := g.removeToStorage("pak", "Su57Navy", join(g.loaderDir(), "Su57Navy"), false); err != nil {
		t.Fatal(err)
	}
	old := g.Storage()

	// refused: inside the game, inside the current storage folder
	if _, err := g.moveStorage(join(g.Root, "Stuff")); err == nil || !strings.Contains(err.Error(), "inside the game folder") {
		t.Fatalf("inside game: %v", err)
	}
	if _, err := g.moveStorage(join(old, "Sub")); err == nil || !strings.Contains(err.Error(), "inside the current storage") {
		t.Fatalf("inside storage: %v", err)
	}

	// an empty folder is used as it is
	empty := join(t.TempDir(), "My AC8 storage")
	os.MkdirAll(empty, 0o755)
	res, err := g.moveStorage(g.storageTarget(empty))
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(g.Storage(), empty) || !samePath(res.Path, empty) || res.Moved < 2 {
		t.Fatalf("%+v storage %s", res, g.Storage())
	}
	if exists(old) {
		t.Fatal("old storage folder left behind")
	}
	if m := g.modeInfo().Mode; m != modeUnmodded {
		t.Fatalf("mode %s", m)
	}
	if !isFile(join(empty, "README - what is this folder.txt")) {
		t.Fatal("no readme in the new folder")
	}
	// the removed mod goes back to the right place, and switching works from the new folder
	rm := g.listRemoved()
	if len(rm) != 1 {
		t.Fatalf("removed %+v", rm)
	}
	if err := g.restoreRemoved(rm[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	var after []string
	for _, e := range treeSnapshot(t, g.Root) {
		if !strings.Contains(e, "loadorder.txt") {
			after = append(after, e)
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("game changed:\n%v\n%v", before, after)
	}

	// a folder with other things in it gets a storage folder inside it
	busyDir := t.TempDir()
	write(t, join(busyDir, "notes.txt"), "x")
	if got := g.storageTarget(busyDir); !samePath(got, join(busyDir, "ACE COMBAT 8 - mod storage")) {
		t.Fatalf("target %s", got)
	}
	// and back to the default
	if _, err := g.moveStorage(""); err != nil {
		t.Fatal(err)
	}
	if !samePath(g.Storage(), g.DefaultStorage()) || g.customStorage() {
		t.Fatalf("storage %s", g.Storage())
	}

	// a chosen folder that has gone missing is noticed
	gone := join(t.TempDir(), "unplugged")
	updateSettings(func(s *Settings) { s.StorageDir, s.StorageGame = gone, g.Root })
	if !g.storageMissing() {
		t.Fatal("missing storage not noticed")
	}
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/api/switch", strings.NewReader(`{"to":"unmodded"}`))
	req.Header.Set("X-Token", token)
	req.Host = "127.0.0.1:0"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "can't be found") {
		t.Fatalf("switch with missing storage: %s", b)
	}
	if m := g.modeInfo().Mode; m != modeModded {
		t.Fatalf("mode %s", m)
	}
}

// ---- Mod Loader 1.3: *_P.pak mods ----------------------------------------------

func installLoader131(t *testing.T, g Game) {
	installModding(t, g)
	write(t, join(g.loaderDir(), "dlls", "main.dll"), "MZ..."+utf16le("1.3.1-ac8-1.1.2.0-ue5.4")+"...")
}

func pakTree(t *testing.T, g Game) {
	ld := g.loaderDir()
	write(t, join(ld, "FontMod", "Font_P.pak"), "pak") // a .pak on its own
	write(t, join(ld, "Loose_P.pak"), "pak")           // loose .pak
	container(t, join(ld, "Both"), "B_P")              // .pak + .utoc + .ucas: one container
	write(t, join(ld, "NoCas_P.pak"), "pak")           // .pak with .utoc but no .ucas
	write(t, join(ld, "NoCas_P.utoc"), "toc")
	container(t, ld, "Plain") // loose .utoc (its .pak is not _P)
	write(t, join(ld, "Folder", "sub", "x.utoc"), "toc")
	write(t, join(ld, "Folder", "sub", "x.ucas"), "cas")
	write(t, join(ld, "Folder", "Ignored.pak"), "pak") // not *_P.pak: never loaded
	write(t, join(g.Paks(), "~mods", "T_P.pak"), "pak")
	write(t, join(ld, "loadorder.txt"), "+Loose_P.pak\r\n-FontMod\r\n")
}

func TestPakModsMatchLoader131(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	pakTree(t, g)
	if !loaderPaks(g.loaderDir()) {
		t.Fatal("1.3.1 not recognised")
	}
	mods, stale := scanPakMods(g.loaderDir(), g.tildeModsDir())
	want := []string{"Loose_P.pak", "FontMod", "Both", "Folder", "NoCas_P.pak", "Plain.utoc", `~mods\T_P.pak`}
	if got := keys(mods); !reflect.DeepEqual(got, want) || len(stale) != 0 {
		t.Fatalf("mods\n got %q\nwant %q (stale %v)", got, want, stale)
	}
	var plan []string
	for _, m := range mods {
		for _, c := range m.Containers {
			if m.Enabled && c.Skip == "" {
				rel, _ := filepath.Rel(g.Root, c.Path)
				plan = append(plan, filepath.ToSlash(rel))
			}
		}
		if m.Key == "Both" && (len(m.Containers) != 1 || m.Containers[0].Name != "B_P.pak") {
			t.Fatalf("Both: %+v", m.Containers)
		}
		if m.Key == "NoCas_P.pak" && !strings.Contains(m.Containers[0].Skip, "NoCas_P.ucas is missing") {
			t.Fatalf("NoCas: %q", m.Containers[0].Skip)
		}
	}
	// with Mod Loader 1.1.0 the same folder shows only .utoc mods, as that loader loads them
	write(t, join(g.loaderDir(), "dlls", "main.dll"), "MZ..."+utf16le("1.1.0-ac8-1.1.2.0-ue5.4")+"...")
	old, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	if got := keys(old); !reflect.DeepEqual(got, []string{"Both", "Folder", "NoCas_P.utoc", "Plain.utoc"}) {
		t.Fatalf("1.1.0 view %q", got)
	}
	write(t, join(g.loaderDir(), "dlls", "main.dll"), "MZ..."+utf16le("1.3.1-ac8-1.1.2.0-ue5.4")+"...")

	// tidying the load order keeps the .pak entries (a switched-off one stays off)
	g.tidyLoadOrder()
	b, _ := os.ReadFile(join(g.loaderDir(), "loadorder.txt"))
	if !strings.Contains(string(b), "+Loose_P.pak") || !strings.Contains(string(b), "-FontMod") || !strings.Contains(string(b), "+~mods\\T_P.pak") {
		t.Fatalf("loadorder:\n%s", b)
	}

	// the real Mod Loader 1.3.1 (built for Linux from its own source) must agree
	bin := os.Getenv("ACE8_LOADER131_SELFTEST")
	if bin == "" {
		t.Skip("ACE8_LOADER131_SELFTEST not set")
	}
	out, err := exec.Command(bin, "--dry", g.loaderDir(), g.Paks()).CombinedOutput()
	if err != nil {
		t.Fatalf("loader dry run: %v\n%s", err, out)
	}
	var loaderPlan []string
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimRight(l, "\r")
		var p string
		switch {
		case strings.HasPrefix(l, "mounting ["):
			p = l[strings.Index(l, "path=")+5:]
		case strings.HasPrefix(l, "  skipped Pak ") && strings.Contains(l, "verified Legacy Mount"):
			// the simulated game has no .pak mount: the loader names each planned .pak here
			p = l[len("  skipped Pak "):strings.Index(l, ": verified")]
		default:
			continue
		}
		rel, _ := filepath.Rel(g.Root, filepath.Clean(strings.ReplaceAll(p, `\`, "/")))
		loaderPlan = append(loaderPlan, filepath.ToSlash(rel))
	}
	if !reflect.DeepEqual(plan, loaderPlan) {
		t.Fatalf("launcher and loader disagree\nlauncher %q\nloader   %q\n%s", plan, loaderPlan, out)
	}
}

func TestPakLog131(t *testing.T) {
	b, err := os.ReadFile("testdata/AC8IoStoreLoader-1.3.1-paks.log")
	if err != nil {
		t.Fatal(err)
	}
	ri := parseRunLog(decodeText(b))
	if ri.Version != "1.3.1" || ri.State == "waiting" {
		t.Fatalf("%+v", ri)
	}
	// Loose: AL=1 in table; fixture: Broken AL=0 out, fail_pair in table, LooseToc ok, Only in table, Pair ok, Deep in table
	if r := ri.Mods["loose_26476_p.pak"]; r == nil || r.Loaded != 1 {
		t.Fatalf("loose %+v", ri.Mods)
	}
	if r := ri.Mods["pakplanfixture_26476"]; r == nil || r.Loaded != 5 || r.Failed != 1 {
		t.Fatalf("fixture %+v", r)
	}
	if ri.Mounted != 6 || ri.Total != 7 || ri.State != "problems" {
		t.Fatalf("summary %d/%d %s", ri.Mounted, ri.Total, ri.State)
	}
	for _, p := range ri.Problems {
		if strings.Contains(p, "native mount table") {
			t.Fatalf("not a problem: %s", p)
		}
	}
}

func TestAddPakMods(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	z := join(t.TempDir(), "English Fonts.zip")
	makeZip(t, z, map[string]string{"English Fonts/Font_P.pak": "pak", "English Fonts/readme.txt": "x"})
	res, err := addFromPaths(g, []string{z}, nil)
	if err != nil || !reflect.DeepEqual(res.Added, []string{"English Fonts"}) || len(res.Notes) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	if got := keys(mods); !reflect.DeepEqual(got, []string{"English Fonts"}) || mods[0].Loads != 1 {
		t.Fatalf("%q", got)
	}
	// a .pak with its .utoc: one container; the .ucas check applies
	z2 := join(t.TempDir(), "UI.zip")
	makeZip(t, z2, map[string]string{"UI_P.pak": "p", "UI_P.utoc": "t"})
	res, err = addFromPaths(g, []string{z2}, nil)
	if err != nil || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "UI_P.pak has no .ucas") {
		t.Fatalf("%+v %v", res, err)
	}
	// a .pak that does not end in _P.pak is explained
	z3 := join(t.TempDir(), "bad.zip")
	makeZip(t, z3, map[string]string{"Font.pak": "p"})
	if _, err := addFromPaths(g, []string{z3}, nil); err == nil || !strings.Contains(err.Error(), "end in _P.pak") {
		t.Fatalf("err %v", err)
	}
	// with Mod Loader 1.1.0 the player is told to update
	write(t, join(g.loaderDir(), "dlls", "main.dll"), "MZ..."+utf16le("1.1.0-ac8-1.1.2.0-ue5.4")+"...")
	z4 := join(t.TempDir(), "Other.zip")
	makeZip(t, z4, map[string]string{"Other/Other_P.pak": "p"})
	res, err = addFromPaths(g, []string{z4}, nil)
	if err != nil || len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "needs Mod Loader 1.3.1") {
		t.Fatalf("%+v %v", res, err)
	}
}

// once modding is set up, the launcher works without its "install files" folder
func TestSetupWithoutInstallFiles(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	ld := join(g.Win64(), "ue4ss", "Mods", "IoStoreLoaderMod")
	os.Remove(join(ld, "enabled.txt"))
	write(t, join(g.Win64(), "ue4ss", "Mods", "mods.txt"), "IoStoreLoaderMod : 0\r\n")
	st := g.setupStatus()
	if st.BundleFound || !st.UE4SS || st.Loader == "" || st.LoaderOn {
		t.Fatalf("%+v", st)
	}
	if _, err := g.setup(); err != nil {
		t.Fatal(err)
	}
	if !g.setupStatus().LoaderOn {
		t.Fatal("Mod Loader not switched on")
	}
	// nothing installed and no install files: a clear error
	g2 := newGame(t)
	if _, err := g2.setup(); err == nil || !strings.Contains(err.Error(), "Copy the Game folder") {
		t.Fatal(err)
	}
	// an older launcher's "install files" folder still works
	bd := join(exeDir, "install files")
	write(t, join(bd, "Game", "Binaries", "Win64", "dwmapi.dll"), "dwm")
	write(t, join(bd, "Game", "Binaries", "Win64", "ue4ss", "UE4SS.dll"), "ue4ss")
	write(t, join(bd, "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "dlls", "main.dll"), utf16le("1.3.1-ac8-1.1.2.0-ue5.4"))
	bundleAt = time.Time{}
	if st := g2.setupStatus(); !st.BundleFound || st.Bundled != "1.3.1" {
		t.Fatalf("%+v", st)
	}
	if _, err := g2.setup(); err != nil || !g2.setupStatus().LoaderOn {
		t.Fatal(err)
	}
}

// Mod Loader 1.3.1 with ordinary mods (_P.pak + .utoc + .ucas), which it loads
// through the .pak route, and one .utoc-only mod
func TestPakTrioLog131(t *testing.T) {
	b, err := os.ReadFile("testdata/AC8IoStoreLoader-1.3.1-pak-trios.log")
	if err != nil {
		t.Fatal(err)
	}
	ri := parseRunLog(decodeText(b))
	if ri.State != "ok" || ri.Mounted != 7 || ri.Total != 7 || len(ri.Mods) != 7 {
		t.Fatalf("%s %s %d/%d", ri.State, ri.Summary, ri.Mounted, ri.Total)
	}
	for k, r := range ri.Mods {
		if r.Loaded != 1 || r.Failed != 0 {
			t.Errorf("%s: %+v", k, *r)
		}
	}
}

// UE4SS starts the script mods; its log says which ones
func TestScriptRunLog(t *testing.T) {
	b, err := os.ReadFile("testdata/UE4SS-3.0.1.log")
	if err != nil {
		t.Fatal(err)
	}
	sr := parseScriptRun(decodeText(b))
	if sr == nil || !sr.Started["ac8stonehenge"] || !sr.Started["iostoreloadermod"] || len(sr.Started) != 2 || len(sr.Problems) != 0 {
		t.Fatalf("%+v", sr)
	}
	sr = parseScriptRun("[x] Starting mods (from mods.txt load order)...\n[x] Starting Lua mod 'SkyLive'\n[x] Starting C++ mod 'RoGLaser'\n" +
		"[x] [Lua] [SkyLive] error: main.lua:3: attempt to index a nil value\n")
	if sr == nil || !sr.Started["skylive"] || !sr.Started["roglaser"] || len(sr.Problems) != 1 || !strings.HasPrefix(sr.Problems[0], "[Lua]") {
		t.Fatalf("%+v", sr)
	}
	if parseScriptRun("[x] Console created\n") != nil {
		t.Fatal("a log without started mods should say nothing")
	}
	// in the state, from the game's ue4ss folder
	g := newGame(t)
	installModding(t, g)
	write(t, join(g.ue4ssRoot(), "UE4SS.log"), string(b))
	if st := buildState(); st.ScriptRun == nil || !st.ScriptRun.Started["ac8stonehenge"] {
		t.Fatalf("%+v", st.ScriptRun)
	}
}
