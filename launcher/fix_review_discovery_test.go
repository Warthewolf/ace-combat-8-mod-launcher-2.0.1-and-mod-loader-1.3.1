package main

// Final review, mod list and logs: ~mods .pak files the game loads itself in
// the Mod Loader's log, the ~mods notes while Unmodded, switching off a script
// mod by the name UE4SS gives it, and the cost of a crafted .utoc, a big
// UE4SS.log and many loose .pak files on Linux.

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"
)

const (
	rdRoot  = `C:\G\Game\Binaries\Win64\ue4ss\Mods\IoStoreLoaderMod`
	rdTilde = `C:\G\Game\Binaries\Win64\../../../Game/Content/Paks\~mods`
)

func rdLog(lines ...string) *RunInfo { return parseRunLog(strings.Join(lines, "\r\n")) }

// ---- the Mod Loader's log: ~mods .pak files the game loaded itself ------------------

// After a game update the Mod Loader can't mount .pak files itself, but its
// .pak hook is in, so the game still loads ~mods\Font_P.pak by itself. Only
// the Mod Loader's own .pak is left out.
func TestRunLogTildePakLoadedWhenPakMountUnavailable(t *testing.T) {
	ri := rdLog(
		"AC8 mod loader 1.3.1",
		"mod root="+rdRoot,
		"Legacy Pak active mounting unavailable: unique entry/native IoStore call not verified; UTOC loading retained",
		"Pak hook installed: only *_P.pak uses unsigned reader; must be installed before its constructor runs",
		"hook installed, waiting for the game to mount pakchunk0-Windows.utoc",
		"custom pak: signedMode=1 -> 0 path=../../../Game/Content/Paks/~mods/Font_P.pak",
		"stock mount code=0 order=3 path=../../../Game/Content/Paks/pakchunk0-Windows.utoc",
		"mods found=2 (loader folder 1, ~mods 1)",
		"~mods folder="+rdTilde,
		"loadorder.txt=found",
		"  on   Jet",
		`  on   ~mods\Font_P.pak`,
		"  skipped Pak "+rdRoot+`\Jet\Jet_P.pak: verified Legacy Mount or native platformFile unavailable`,
		"  skipped Pak "+rdTilde+`\Font_P.pak: verified Legacy Mount or native platformFile unavailable`,
		"done: 0 of 0 attempted mount requests returned success (2 planned, 2 mod(s))",
	)
	if r := ri.Mods[`~mods\font_p.pak`]; r == nil || r.Loaded != 1 || r.Failed != 0 || !strings.Contains(strings.Join(r.Messages, " "), "Loaded by the game itself") {
		t.Fatalf("~mods\\Font_P.pak: %+v", r)
	}
	if r := ri.Mods["jet"]; r == nil || r.Failed != 1 || !strings.Contains(r.Messages[0], "can't load .pak mods on this game version yet") {
		t.Fatalf("Jet: %+v", r)
	}
	if ri.Mounted != 1 || ri.Total != 2 || !strings.HasPrefix(ri.Summary, "1 of 2 mod files loaded.") || !strings.Contains(ri.Summary, "1 file was left out") {
		t.Fatalf("%d of %d: %q", ri.Mounted, ri.Total, ri.Summary)
	}
}

// The deferred pass: first "unavailable" (the game's table can't be read yet),
// then the retry finds the ~mods .pak in the game's table. The retry counts.
func TestRunLogTildePakDeferredThenInTable(t *testing.T) {
	ri := rdLog(
		"AC8 mod loader 1.3.1",
		"mod root="+rdRoot,
		"hook installed, waiting for the game to mount pakchunk0-Windows.utoc",
		"stock mount code=0 order=3 path=../../../Game/Content/Paks/pakchunk0-Windows.utoc",
		"mods found=1 (loader folder 0, ~mods 1)",
		"~mods folder="+rdTilde,
		`  on   ~mods\Font_P.pak`,
		"  skipped Pak "+rdTilde+`\Font_P.pak: verified Legacy Mount or native platformFile unavailable`,
		"done: 0 of 0 attempted mount requests returned success (1 planned, 1 mod(s))",
		"Legacy Pak: deferred until a verified native platformFile is captured; UTOC results retained",
		"Legacy Pak: native object is now available; retrying deferred Pak plan only",
		"mods found=1 (loader folder 0, ~mods 1)",
		"~mods folder="+rdTilde,
		`  on   ~mods\Font_P.pak`,
		"  skipped Pak "+rdTilde+`\Font_P.pak: already present in the native mount table`,
		"done: 0 of 0 attempted mount requests returned success (1 planned, 1 mod(s))",
	)
	if r := ri.Mods[`~mods\font_p.pak`]; r == nil || r.Loaded != 1 || r.Failed != 0 {
		t.Fatalf("~mods\\Font_P.pak: %+v", r)
	}
	if ri.State != "ok" || ri.Summary != "1 of 1 mod files loaded." || len(ri.Problems) != 0 {
		t.Fatalf("%s %q %q", ri.State, ri.Summary, ri.Problems)
	}
}

// The same pure .pak in ~mods and, newer, in the Mod Loader's folder. The game
// loads the ~mods copy itself; the Mod Loader's copy is the one left out.
func TestRunLogTildePakSameNameAsNewerCopy(t *testing.T) {
	ri := rdLog(
		"AC8 mod loader 1.3.1",
		"mod root="+rdRoot,
		"hook installed, waiting for the game to mount pakchunk0-Windows.utoc",
		"custom pak: signedMode=1 -> 0 path=../../../Game/Content/Paks/~mods/Font_P.pak",
		"stock mount code=0 order=3 path=../../../Game/Content/Paks/pakchunk0-Windows.utoc",
		"mods found=2 (loader folder 1, ~mods 1)",
		"~mods folder="+rdTilde,
		"  on   Font",
		`  on   ~mods\Font_P.pak`,
		"  skipped "+rdTilde+`\Font_P.pak: same name as `+rdRoot+`\Font\Font_P.pak, and only one can load (the newer one is used)`,
		"  skipped Pak "+rdRoot+`\Font\Font_P.pak: already present in the native mount table`,
		"done: 0 of 0 attempted mount requests returned success (1 planned, 1 mod(s))",
	)
	if r := ri.Mods[`~mods\font_p.pak`]; r == nil || r.Loaded != 1 || r.Failed != 0 {
		t.Fatalf("~mods\\Font_P.pak: %+v", r)
	}
	if n := ri.notLoaded["font"]; len(n) != 1 || !strings.Contains(n[0].text, "the game already had another file named Font_P.pak") {
		t.Fatalf("Font: %+v", n)
	}
	if ri.Mounted != 1 || ri.Total != 2 || ri.State != "problems" {
		t.Fatalf("%d of %d, %s", ri.Mounted, ri.Total, ri.State)
	}
}

// The Mod Loader mounted the newer copy itself: then the game's table didn't
// have that name, so the ~mods copy wasn't loaded after all.
func TestRunLogTildePakSameNameNewerMounted(t *testing.T) {
	ri := rdLog(
		"AC8 mod loader 1.3.1",
		"mod root="+rdRoot,
		"Pak hook installed: only *_P.pak uses unsigned reader; must be installed before its constructor runs",
		"hook installed, waiting for the game to mount pakchunk0-Windows.utoc",
		"custom pak: signedMode=1 -> 0 path=../../../Game/Content/Paks/~mods/Font_P.pak",
		"stock mount code=0 order=3 path=../../../Game/Content/Paks/pakchunk0-Windows.utoc",
		"mods found=2 (loader folder 1, ~mods 1)",
		"~mods folder="+rdTilde,
		"  skipped "+rdTilde+`\Font_P.pak: same name as `+rdRoot+`\Font\Font_P.pak, and only one can load (the newer one is used)`,
		"mounting Legacy Pak [Font] order=1000 path="+rdRoot+`\Font\Font_P.pak`,
		"custom pak: signedMode=1 -> 0 path="+rdRoot+`\Font\Font_P.pak`,
		"  Legacy result AL=1 pak-in-table=1 (paired UTOC registration is owned by the engine)",
		"done: 1 of 1 attempted mount requests returned success (1 planned, 1 mod(s))",
	)
	if r := ri.Mods[`~mods\font_p.pak`]; r != nil {
		t.Fatalf("~mods\\Font_P.pak: %+v", r)
	}
	if r := ri.Mods["font"]; r == nil || r.Loaded != 1 || ri.Mounted != 1 || ri.Total != 2 {
		t.Fatalf("Font: %+v, %d of %d", r, ri.Mounted, ri.Total)
	}
}

// When the Mod Loader started after the game had opened its .pak files, its
// hook wasn't there for ~mods: such a .pak still counts as not loaded.
func TestRunLogTildePakStartedLateStaysFailed(t *testing.T) {
	ri := rdLog(
		"AC8 mod loader 1.3.1",
		"mod root="+rdRoot,
		"Legacy Pak active mounting unavailable: unique entry/native IoStore call not verified; UTOC loading retained",
		"Pak hook installed: only *_P.pak uses unsigned reader; must be installed before its constructor runs",
		"hook installed, waiting for the game to mount pakchunk0-Windows.utoc",
		"stock mount code=0 order=3 path=../../../Game/Content/Paks/pakchunk0optional-Windows.utoc",
		"the loader started after the game mounted pakchunk0-Windows.utoc; mounting the mods now, with pakchunk0optional-Windows.utoc",
		"mods found=1 (loader folder 0, ~mods 1)",
		"~mods folder="+rdTilde,
		`  on   ~mods\Font_P.pak`,
		"  skipped Pak "+rdTilde+`\Font_P.pak: verified Legacy Mount or native platformFile unavailable`,
		"done: 0 of 0 attempted mount requests returned success (1 planned, 1 mod(s))",
	)
	if r := ri.Mods[`~mods\font_p.pak`]; r == nil || r.Loaded != 0 || r.Failed != 1 {
		t.Fatalf("~mods\\Font_P.pak: %+v", r)
	}
	if ri.State != "problems" || !strings.HasPrefix(ri.Summary, "0 of 1 mod files loaded.") {
		t.Fatalf("%s %q", ri.State, ri.Summary)
	}
}

// ---- ~mods notes while Unmodded -----------------------------------------------------

// Unmodded: ~mods is in the mod storage folder, where the game can't read it,
// so no note may say that a ~mods .pak still loads. Back in Modded it does.
func TestUnmoddedTildePakNotesSayStorage(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	write(t, join(g.Paks(), "~mods", "Font_P.pak"), "pak")
	write(t, join(g.Paks(), "~mods", "Hud_P.pak"), "pak")
	write(t, join(g.loaderDir(), "loadorder.txt"), "-~mods\\Font_P.pak\r\n+~mods\\Hud_P.pak\r\n")
	notes := func() map[string][]Note {
		t.Helper()
		out := map[string][]Note{}
		for _, m := range buildState().PakMods {
			out[m.Key] = m.Notes
		}
		return out
	}
	modded := notes()
	if n := modded[`~mods\Font_P.pak`]; len(n) != 1 || n[0].Kind != "warn" || !strings.HasPrefix(n[0].Text, "Still loads") {
		t.Fatalf("Modded, switched off: %+v", n)
	}
	if _, err := g.switchTo(modeUnmodded); err != nil {
		t.Fatal(err)
	}
	if g.modeInfo().Mode != modeUnmodded || !isInside(g.tildeModsDir(), g.OffDir()) {
		t.Fatalf("%s %s", g.modeInfo().Mode, g.tildeModsDir())
	}
	unmodded := notes()
	for _, key := range []string{`~mods\Font_P.pak`, `~mods\Hud_P.pak`} {
		n := unmodded[key]
		if len(n) != 1 || n[0].Kind != "info" || !strings.HasPrefix(n[0].Text, "In storage now, so the game doesn't load it.") ||
			!strings.Contains(n[0].Text, "When you switch back to Modded") || strings.Contains(n[0].Text, "Still loads") {
			t.Errorf("Unmodded, %s: %+v", key, n)
		}
	}
	if n := unmodded[`~mods\Font_P.pak`]; len(n) == 1 && !strings.Contains(n[0].Text, "even though it is switched off") {
		t.Errorf("Unmodded, switched off: %+v", n)
	}
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	if n := notes()[`~mods\Font_P.pak`]; len(n) != 1 || !strings.HasPrefix(n[0].Text, "Still loads") {
		t.Fatalf("Modded again: %+v", n)
	}
}

// ---- script mods: the name UE4SS gives them ------------------------------------------

// Folder "AC8.Stonehenge" is "AC8" for UE4SS, and mods.txt "AC8 : 1" starts it.
// Switching it off must change that line, or UE4SS keeps starting it.
func TestDottedScriptModSwitchOffChangesUE4SSLine(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	md := join(g.ue4ssRoot(), "Mods")
	write(t, join(md, "AC8.Stonehenge", "Scripts", "main.lua"), "")
	write(t, join(md, "AC8.Stonehenge", "enabled.txt"), "")
	write(t, join(md, "mods.txt"), "IoStoreLoaderMod : 1\r\nAC8 : 1\r\n  Other:1\r\n; Built-in keybinds, do not move up!\r\nKeybinds : 1\r\n")
	on := func() bool {
		t.Helper()
		for _, m := range listScriptMods(g.ue4ssRoot()) {
			if m.Name == "AC8.Stonehenge" {
				return m.Enabled
			}
		}
		t.Fatal("AC8.Stonehenge is not listed")
		return false
	}
	if !on() {
		t.Fatal("not on to begin with")
	}
	if err := setScriptMod(g.ue4ssRoot(), "AC8.Stonehenge", false); err != nil {
		t.Fatal(err)
	}
	if got := readS(t, join(md, "mods.txt")); got != "IoStoreLoaderMod : 1\r\nAC8 : 0\r\n  Other:1\r\n; Built-in keybinds, do not move up!\r\nKeybinds : 1\r\n" {
		t.Fatalf("mods.txt after Off: %q", got)
	}
	if on() || ue4ssModsTxt(md)["AC8"] || isFile(join(md, "AC8.Stonehenge", "enabled.txt")) {
		t.Fatal("still on after Off")
	}
	if err := setScriptMod(g.ue4ssRoot(), "AC8.Stonehenge", true); err != nil {
		t.Fatal(err)
	}
	if got := readS(t, join(md, "mods.txt")); !strings.Contains(got, "\r\nAC8 : 1\r\n") || !on() {
		t.Fatalf("mods.txt after On: %q", got)
	}
}

// A line UE4SS reads as the mod's name in another spelling ("Sky Live" is
// "SkyLive" to UE4SS) is switched too, and keeps its spelling.
func TestScriptModSwitchOffKeepsLineSpelling(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	md := join(g.ue4ssRoot(), "Mods")
	write(t, join(md, "SkyLive", "Scripts", "main.lua"), "")
	write(t, join(md, "mods.txt"), "Sky Live : 1\r\nskylive : 1\r\nKeybinds : 1\r\n")
	if err := setScriptMod(g.ue4ssRoot(), "SkyLive", false); err != nil {
		t.Fatal(err)
	}
	// "skylive" is the folder's name in other capitals: written as the folder's
	// name, as before. UE4SS matches case, so it only ever read "Sky Live".
	if got := readS(t, join(md, "mods.txt")); got != "Sky Live : 0\r\nSkyLive : 0\r\nKeybinds : 1\r\n" {
		t.Fatalf("%q", got)
	}
	if ue4ssModsTxt(md)["SkyLive"] {
		t.Fatal("UE4SS still reads it as on")
	}
}

// ---- a crafted .utoc: one long name named by every entry ------------------------------

// rdLongNameUtoc: a .utoc of about size bytes whose file entries (files) or
// folders (dirs) all name one string of nearly 64 KB.
func rdLongNameUtoc(size int, dirs bool) []byte {
	long := strings.Repeat("a", maxUtocNameLen-2)
	n := (size - len(long)) / 16
	var ds []fxDir
	var fs []fxFile
	if dirs {
		ds = append(ds, fxDir{fxNone, 1, fxNone, fxNone})
		for i := 1; i <= n; i++ {
			next := uint32(i + 1)
			if i == n {
				next = fxNone
			}
			ds = append(ds, fxDir{0, fxNone, next, 0})
		}
		fs = []fxFile{{0, fxNone, 0}}
	} else {
		ds = []fxDir{{fxNone, fxNone, fxNone, 0}}
		for i := 0; i < n; i++ {
			next := uint32(i + 1)
			if i == n-1 {
				next = fxNone
			}
			fs = append(fs, fxFile{0, next, 0})
		}
	}
	return fxUtoc([]uint64{7}, nil, fxIndex("../../../Game/", ds, fs, []string{long}))
}

// Every entry was a 64 KB string copy before its length was checked: a 1 MB
// file took seconds and gigabytes. The memory used now stays near the size.
func TestUtocLongNamesCostLittle(t *testing.T) {
	for _, dirs := range []bool{false, true} {
		d := rdLongNameUtoc(1<<20, dirs)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		u, err := parseUtoc(d)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatal(err)
		}
		if len(u.files) != 0 {
			t.Fatalf("dirs=%v: %d names kept", dirs, len(u.files))
		}
		if used := after.TotalAlloc - before.TotalAlloc; used > 64<<20 {
			t.Fatalf("dirs=%v: %d MB used for a %d KB .utoc", dirs, used>>20, len(d)>>10)
		}
	}
	// names that fit are still read as before
	u, err := parseUtoc(fxGoodUtoc())
	if err != nil || u.files[0] != "../../../Game/Content/Vehicles/A.uasset" || u.files[1] != "../../../Game/Content/Vehicles/Planes/B.uasset" {
		t.Fatalf("%v %v", err, u.files)
	}
}

func BenchmarkParseUtocLongNames(b *testing.B) {
	d := rdLongNameUtoc(4<<20, false)
	b.SetBytes(int64(len(d)))
	for i := 0; i < b.N; i++ {
		parseUtoc(d)
	}
}

// ---- a big UE4SS.log ---------------------------------------------------------------

// The quick look before reScriptProblem never leaves out a line it matches:
// none of the words it looks for has a letter that also matches a non-ASCII
// one in reScriptProblem's (?i), and each alternative needs one of them.
func TestScriptProblemPrefilterMatchesRegexp(t *testing.T) {
	for _, w := range []string{"error", "exception", "execute_file", "fail", "could not load", "lua_pcall", "lual_loadfile", "not found"} {
		for _, r := range w {
			for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
				if f > unicode.MaxASCII {
					t.Errorf("%q in %q also matches %q", r, w, f)
				}
			}
		}
	}
	lines := []string{
		"[Lua] [SkyLive] error in tick", "ERROR from [Lua]", "Failed to load dll for mod X", "failed to load mod Y",
		"could not load something", "LUA_PCALL returned 2", "luaL_loadfile returned 1", "[Lua::execute_file] luaL_loadfile returned 7",
		"Error loading script: main.lua:3: x", "Error executing script", "Failed to execute main script", "Main script 'main.lua' not found",
		"Exception during mod startup", "exception during mod preparation", "Exception in load_and_execute_script", "Failed to find exported mod lifecycle functions",
		"[Lua] [SkyLive] tick 1: altitude=3", "Starting Lua mod 'SkyLive'", "plain text", "", "Main \u017fcript 'main.lua' not found", "[\u212aua] error",
		"\xff\xfe error", "fai", "erro",
	}
	for _, base := range lines {
		for _, l := range []string{base, strings.ToUpper(base), strings.ToLower(base), "[12:00:00] " + base + " \xff"} {
			if reScriptProblem.MatchString(l) && !mayBeScriptProblem(l) {
				t.Errorf("the quick look leaves out %q", l)
			}
		}
	}
}

// The same answer as reading every line with every pattern, on a log with
// starts, problems and many print lines.
func TestParseScriptRunSameWithQuickLook(t *testing.T) {
	text := "[a] Mod 'SkyLive' has enabled.txt, starting mod.\r\n" +
		"[b] Starting Lua mod 'Mobius1's_HUD'\r\n" +
		"[c] Starting C++ mod 'CppThing'\r\n" +
		strings.Repeat("[d] [Lua] [SkyLive] tick 1: speed=3\r\n", 50) +
		"[e] [Lua] [SkyLive] ERROR: attempt to index nil\r\n" +
		"[f] Error loading script: C:\\G\\ue4ss\\Mods\\Broken\\Scripts\\main.lua:3: unexpected symbol\r\n" +
		"[g] Main script 'main.lua' not found for mod NoMain\r\n" +
		"[h] something failed to load DLL for mod Cpp\r\n"
	sr := parseScriptRun(text)
	if sr == nil || !sr.Started["skylive"] || !sr.Started["mobius1's_hud"] || !sr.Started["cppthing"] || len(sr.Problems) != 4 {
		t.Fatalf("%+v", sr)
	}
	for i, want := range []string{"SkyLive has an error in its script", "Broken has an error in its script", "NoMain can't start", "Cpp can't start"} {
		if !strings.HasPrefix(sr.Problems[i], want) {
			t.Errorf("problem %d: %q", i, sr.Problems[i])
		}
	}
}

// readScriptRun keeps what it read from a log that hasn't changed, and gives
// each caller its own copy; a changed or just written log is read again.
func TestReadScriptRunKeepsUnchangedLog(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	md := join(g.ue4ssRoot(), "Mods")
	write(t, join(md, "AC8.Sky", "Scripts", "main.lua"), "")
	write(t, join(md, "AC8.Hud", "Scripts", "main.lua"), "")
	logPath := join(g.ue4ssRoot(), "UE4SS.log")
	old := time.Now().Add(-time.Hour)
	write(t, logPath, "[x] Mod 'AC8' has enabled.txt, starting mod.\n")
	os.Chtimes(logPath, old, old)
	for i := 0; i < 3; i++ {
		sr := readScriptRun(g.ue4ssRoot())
		if sr == nil || !sr.Started["ac8.sky"] || !sr.Started["ac8.hud"] || sr.Started["changed"] || len(sr.Problems) != 1 || !sr.When.Equal(old) {
			t.Fatalf("read %d: %+v", i, sr)
		}
		if lastScriptRun.path != logPath {
			t.Fatalf("not kept: %q", lastScriptRun.path)
		}
		sr.Started["changed"] = true
		sr.Problems[0] = "changed"
	}
	// written again: read again, also when the size stays the same
	write(t, logPath, "[x] Mod 'BC8' has enabled.txt, starting mod.\n")
	if sr := readScriptRun(g.ue4ssRoot()); sr == nil || sr.Started["ac8"] || !sr.Started["bc8"] {
		t.Fatalf("after writing it again: %+v", sr)
	}
	write(t, logPath, "[x] Mod 'CC8' has enabled.txt, starting mod.\n")
	if sr := readScriptRun(g.ue4ssRoot()); sr == nil || !sr.Started["cc8"] {
		t.Fatalf("written again within a moment: %+v", sr)
	}
	newer := old.Add(time.Minute)
	write(t, logPath, "[x] nothing started\n")
	os.Chtimes(logPath, newer, newer)
	if sr := readScriptRun(g.ue4ssRoot()); sr != nil {
		t.Fatalf("no mod started: %+v", sr)
	}
}

func rdBigUE4SSLog(mb int) string {
	var b strings.Builder
	b.WriteString("[12:00:00.0000000] Mod 'SkyLive' has enabled.txt, starting mod.\r\n")
	for i := 0; b.Len() < mb<<20; i++ {
		fmt.Fprintf(&b, "[12:00:%02d.%07d] [Lua] [SkyLive] tick %d: altitude=%d speed=%d heading=%d target=none\r\n", i%60, i, i, i*7%9000, i*3%1400, i%360)
	}
	return b.String()
}

func BenchmarkParseScriptRunBigLog(b *testing.B) {
	text := rdBigUE4SSLog(20)
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		parseScriptRun(text)
	}
}

// a 20 MB UE4SS.log of a mod's error every frame, read again for each state
// load after the first one since the game closed
func BenchmarkReadScriptRunErrorLogAgain(b *testing.B) {
	root := b.TempDir()
	var sb strings.Builder
	sb.WriteString("[12:00:00.0000000] Mod 'SkyLive' has enabled.txt, starting mod.\r\n")
	for i := 0; sb.Len() < 20<<20; i++ {
		fmt.Fprintf(&sb, "[12:00:%02d.%07d] [Lua] [SkyLive] error in tick %d: attempt to index a nil value (field 'target')\r\n", i%60, i, i)
	}
	os.WriteFile(join(root, "UE4SS.log"), []byte(sb.String()), 0o644)
	old := time.Now().Add(-time.Minute)
	os.Chtimes(join(root, "UE4SS.log"), old, old)
	readScriptRun(root)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if sr := readScriptRun(root); sr == nil || len(sr.Problems) != 1 {
			b.Fatal(sr)
		}
	}
}

// ---- many loose .pak files on Linux ----------------------------------------------------

// foldKey groups names exactly as strings.EqualFold does, also for letters
// with more than two forms (k, K and the Kelvin sign; s, S and long s).
func TestFoldKeyIsEqualFold(t *testing.T) {
	names := []string{"Mod_P.pak", "MOD_P.PAK", "mod_p.pak", "Mod_P.utoc", "\u212a.utoc", "k.UTOC", "K.utoc",
		"\u017fky.pak", "SKY.pak", "sky.pak", "Ä.pak", "ä.pak", "\xff.pak", "\xfe.pak", "", "Mod_P.pa", "Straße", "STRASSE"}
	for _, a := range names {
		for _, b := range names {
			if (foldKey(a) == foldKey(b)) != strings.EqualFold(a, b) {
				t.Errorf("%q %q: foldKey %v, EqualFold %v", a, b, foldKey(a) == foldKey(b), strings.EqualFold(a, b))
			}
		}
	}
}

// Looking up files in other capitals through a scan's folderNames finds the
// same file as reading the folder each time: the first match, A to Z.
func TestFolderNamesFindLikeFoldPath(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"MOD_P.UTOC", "mod_p.utoc", "Mod_P.ucas", "Sub/Deep/X.TXT", "\u212a.utoc"} {
		write(t, join(dir, n), "x")
	}
	fn := folderNames{}
	for _, q := range [][]string{{"Mod_P.utoc"}, {"mod_p.UCAS"}, {"mod_p.pak"}, {"sub", "deep", "x.txt"}, {"SUB", "nope"}, {"k.utoc"}, {"nothere", "x"}} {
		for round := 0; round < 2; round++ {
			if got, want := fn.foldPath(dir, q...), foldPath(dir, q...); got != want {
				t.Errorf("%v: %q, want %q", q, got, want)
			}
		}
	}
	if runtime.GOOS != "windows" && fn.foldPath(dir, "mod_P.utoc") != join(dir, "MOD_P.UTOC") {
		t.Errorf("not the first match: %q", fn.foldPath(dir, "mod_P.utoc"))
	}
}

// A scan of loose ~mods .pak files with their .utoc and .ucas in other
// capitals finds them through the scan's folderNames as before.
func TestScanFindsSiblingsInOtherCapitals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ignores case itself")
	}
	g := newGame(t)
	installLoader131(t, g)
	tm := join(g.Paks(), "~mods")
	write(t, join(tm, "Font_P.pak"), "p")
	write(t, join(tm, "FONT_P.UTOC"), string(fxGoodUtoc()))
	write(t, join(tm, "font_p.ucas"), "cc")
	write(t, join(tm, "Lone_P.pak"), "p")
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	if len(mods) != 2 || mods[0].Key != `~mods\Font_P.pak` || mods[1].Key != `~mods\Lone_P.pak` {
		t.Fatalf("%v", keys(mods))
	}
	if c := mods[0].Containers[0]; c.Size != int64(len(fxGoodUtoc())+3) || len(c.Files) != 2 || c.Skip != "" {
		t.Fatalf("%+v", c)
	}
	if c := mods[1].Containers[0]; c.Size != 1 || c.Skip != "" {
		t.Fatalf("%+v", c)
	}
}

// rdScanDirs: a Mod Loader 1.3.1 folder with trios mod folders (.pak, .utoc
// and .ucas) and a ~mods folder with loose .pak files on their own.
func rdScanDirs(b *testing.B, trios, loose int) (ld, tm string) {
	base := b.TempDir()
	ld, tm = join(base, "IoStoreLoaderMod"), join(base, "~mods")
	os.MkdirAll(join(ld, "dlls"), 0o755)
	os.WriteFile(join(ld, "dlls", "main.dll"), []byte("MZ..."+utf16le("1.3.1-ac8-1.1.2.0-ue5.4")+"..."), 0o644)
	for i := 0; i < trios; i++ {
		dir := join(ld, fmt.Sprintf("Skin%03d", i))
		os.MkdirAll(dir, 0o755)
		os.WriteFile(join(dir, fmt.Sprintf("Skin%03d_P.utoc", i)), fxGoodUtoc(), 0o644)
		os.WriteFile(join(dir, fmt.Sprintf("Skin%03d_P.ucas", i)), []byte("cas"), 0o644)
		os.WriteFile(join(dir, fmt.Sprintf("Skin%03d_P.pak", i)), []byte("pak"), 0o644)
	}
	os.MkdirAll(tm, 0o755)
	for i := 0; i < loose; i++ {
		os.WriteFile(join(tm, fmt.Sprintf("Mod%04d_P.pak", i)), []byte("pak"), 0o644)
	}
	return ld, tm
}

func rdBenchScan(b *testing.B, trios, loose int) {
	ld, tm := rdScanDirs(b, trios, loose)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if mods, _ := scanPakMods(ld, tm); len(mods) != trios+loose {
			b.Fatal(len(mods))
		}
	}
}

// 1000 loose ~mods .pak files: on Linux each missing .utoc or .ucas used to
// read the whole folder again, and every pair of files was compared.
func BenchmarkScanManyLoosePaks(b *testing.B) { rdBenchScan(b, 0, 1000) }

// a usual set of mods: the common case must stay as fast
func BenchmarkScanUsualMods(b *testing.B) { rdBenchScan(b, 40, 10) }
