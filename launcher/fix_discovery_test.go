package main

// Regression tests for mod discovery and log reading: damaged .utoc files,
// what the Mod Loader's and UE4SS's logs say, ~mods .pak files the game loads
// by itself, the skin list, conflicts, script mod names and on/off, and file
// names in other capitals (Linux).

import (
	"encoding/binary"
	"encoding/json"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---- building .utoc files ----------------------------------------------------------

const fxNone = 0xFFFFFFFF

func fxU32(vals ...uint32) []byte {
	b := make([]byte, 4*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint32(b[4*i:], v)
	}
	return b
}

func fxStr(s string) []byte {
	return append(append(fxU32(uint32(len(s)+1)), s...), 0)
}

type fxDir struct{ name, child, sibling, file uint32 }
type fxFile struct{ name, next, data uint32 }

func fxIndex(mount string, dirs []fxDir, files []fxFile, strs []string) []byte {
	b := fxStr(mount)
	b = append(b, fxU32(uint32(len(dirs)))...)
	for _, d := range dirs {
		b = append(b, fxU32(d.name, d.child, d.sibling, d.file)...)
	}
	b = append(b, fxU32(uint32(len(files)))...)
	for _, f := range files {
		b = append(b, fxU32(f.name, f.next, f.data)...)
	}
	b = append(b, fxU32(uint32(len(strs)))...)
	for _, s := range strs {
		b = append(b, fxStr(s)...)
	}
	return b
}

// fxUtoc builds a version 3 .utoc: one chunk per id (type 1, one per package,
// unless types says otherwise) and, when idx is given, a directory index.
func fxUtoc(ids []uint64, types []byte, idx []byte) []byte {
	le := binary.LittleEndian
	h := make([]byte, 144)
	copy(h, utocMagic)
	h[16] = 3
	le.PutUint32(h[20:], 144)
	le.PutUint32(h[24:], uint32(len(ids)))
	le.PutUint32(h[48:], uint32(len(idx)))
	if idx != nil {
		h[80] = 0x8 // indexed
	}
	for i, id := range ids {
		e := make([]byte, 12)
		le.PutUint64(e, id)
		e[11] = 1
		if i < len(types) {
			e[11] = types[i]
		}
		h = append(h, e...)
	}
	h = append(h, make([]byte, 10*len(ids))...)
	return append(h, idx...)
}

// a small, valid .utoc: Vehicles/A and Vehicles/Planes/B (and B's .ubulk)
func fxGoodUtoc() []byte {
	idx := fxIndex("../../../Game/Content/",
		[]fxDir{{fxNone, 1, fxNone, fxNone}, {0, 2, fxNone, 0}, {1, fxNone, fxNone, 1}},
		[]fxFile{{2, fxNone, 0}, {3, 2, 1}, {4, fxNone, 2}},
		[]string{"Vehicles", "Planes", "A.uasset", "B.uasset", "B.ubulk"})
	return fxUtoc([]uint64{10, 11, 12}, []byte{1, 1, 2}, idx)
}

// within runs f and fails the test if it takes longer than d (a hang).
func within(t *testing.T, d time.Duration, what string, f func()) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		f()
	}()
	select {
	case p := <-done:
		if p != nil {
			t.Fatalf("%s panicked: %v", what, p)
		}
	case <-time.After(d):
		t.Fatalf("%s still running after %v", what, d)
	}
}

func TestUtocGoodFile(t *testing.T) {
	u, err := parseUtoc(fxGoodUtoc())
	if err != nil {
		t.Fatal(err)
	}
	if got := u.assetNames(); !reflect.DeepEqual(got, []string{"Vehicles/A", "Vehicles/Planes/B"}) {
		t.Fatalf("%q", got)
	}
	if ids := u.packageIDs(); len(ids) != 2 || !ids[10] || !ids[11] {
		t.Fatalf("%v", ids)
	}
}

// utoc-1: a directory index whose links loop back made the walk branch at
// every level and never finish, which held the busy lock for good.
func TestUtocLoopedIndexReturns(t *testing.T) {
	// the reviewer's 198-byte file: dir 1 is its own child and its own sibling
	idx := fxIndex("", []fxDir{{fxNone, 1, fxNone, fxNone}, {0, 1, 1, fxNone}}, nil, []string{"a"})
	within(t, 2*time.Second, "parseUtoc (self loop)", func() { parseUtoc(fxUtoc(nil, nil, idx)) })

	// every folder loops to the root, and files loop to themselves
	dirs := []fxDir{{fxNone, 1, fxNone, 0}}
	for i := uint32(1); i < 40; i++ {
		dirs = append(dirs, fxDir{0, 0, i + 1, 0})
	}
	idx = fxIndex("m/", dirs, []fxFile{{0, 0, 0}}, []string{"x"})
	within(t, 2*time.Second, "parseUtoc (loops to the root)", func() { parseUtoc(fxUtoc([]uint64{1}, nil, idx)) })

	// a long chain where each folder's child is the next one and each sibling
	// chain runs to the end: quadratic work before, linear now
	const n = 60000
	dirs = make([]fxDir, n)
	dirs[0] = fxDir{fxNone, 1, fxNone, fxNone}
	for i := uint32(1); i < n; i++ {
		next := i + 1
		if next == n {
			next = fxNone
		}
		dirs[i] = fxDir{0, next, next, fxNone}
	}
	idx = fxIndex("", dirs, nil, []string{"d"})
	within(t, 3*time.Second, "parseUtoc (long chain)", func() { parseUtoc(fxUtoc(nil, nil, idx)) })
}

// utoc-1 as the player met it: the mod list must still come up.
func TestUtocLoopedIndexBuildState(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	idx := fxIndex("", []fxDir{{fxNone, 1, fxNone, fxNone}, {0, 1, 1, fxNone}}, nil, []string{"a"})
	write(t, join(g.loaderDir(), "Cyc", "Cyc.utoc"), string(fxUtoc(nil, nil, idx)))
	write(t, join(g.loaderDir(), "Cyc", "Cyc.ucas"), "c")
	var st State
	within(t, 5*time.Second, "buildState", func() { st = buildState() })
	if len(st.PakMods) != 1 || st.PakMods[0].Key != "Cyc" {
		t.Fatalf("%q", keys(st.PakMods))
	}
}

func fxHeader(ver byte, blocks, blockSize, methods, methodLen, dirIndexSize uint32, flags byte) []byte {
	h := fxUtoc(nil, nil, nil)
	le := binary.LittleEndian
	h[16] = ver
	le.PutUint32(h[28:], blocks)
	le.PutUint32(h[32:], blockSize)
	le.PutUint32(h[36:], methods)
	le.PutUint32(h[40:], methodLen)
	le.PutUint32(h[48:], dirIndexSize)
	h[80] = flags
	return h
}

// utoc-2: header values whose sum wrapped the offset made parseUtoc panic,
// and every state request with it.
func TestUtocHeaderOverflow(t *testing.T) {
	cases := map[string][]byte{
		"methods wrap":      fxHeader(3, 2, 0xFFFFFFB7, 0xFFFFFFFF, 0xFFFFFFFF, 2, 0x8),
		"signed wrap":       fxHeader(3, 0, 0, 0xFFFFFFFF, 0xFFFFFFFF, 2, 0x8|0x4),
		"dir index too big": fxHeader(3, 0, 0, 0, 0, 0xFFFFFFFF, 0x8),
		"blocks past end":   fxHeader(3, 1<<26, 12, 0, 0, 2, 0x8),
		"v5 counts past end": func() []byte {
			h := fxHeader(5, 0, 0, 0, 0, 2, 0x8)
			binary.LittleEndian.PutUint32(h[84:], 0xFFFFFFFF)
			binary.LittleEndian.PutUint32(h[96:], 0xFFFFFFFF)
			return h
		}(),
	}
	for name, d := range cases {
		within(t, 2*time.Second, name, func() { parseUtoc(d) })
	}
	g := newGame(t)
	installLoader131(t, g)
	write(t, join(g.loaderDir(), "Bad", "Bad_P.utoc"), string(cases["methods wrap"]))
	write(t, join(g.loaderDir(), "Bad", "Bad_P.ucas"), "c")
	within(t, 5*time.Second, "buildState", func() { buildState() })
}

// utoc-2: counts from the file are checked against the bytes really there
// before anything is allocated for them.
func TestUtocHugeCountsAllocateLittle(t *testing.T) {
	for _, idx := range [][]byte{
		append(fxStr(""), fxU32(1<<20)...),                                                                           // a million folders in 8 bytes
		append(append(fxStr(""), fxU32(1)...), fxU32(0, 0, 0, 0, 1<<22)...),                                          // 4M files
		append(append(fxStr(""), fxU32(1)...), append(fxU32(fxNone, fxNone, fxNone, fxNone, 0), fxU32(1<<22)...)...), // 4M strings
	} {
		d := fxUtoc(nil, nil, idx)
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		parseUtoc(d)
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; n > 1<<20 {
			t.Errorf("a %d-byte .utoc made parseUtoc allocate %d bytes", len(d), n)
		}
	}
	// paths have a budget: 40,000 folders with 1,000-character names
	const n = 40000
	dirs := make([]fxDir, n)
	files := make([]fxFile, n)
	ids := make([]uint64, n)
	dirs[0] = fxDir{fxNone, 1, fxNone, fxNone}
	for i := uint32(1); i < n; i++ {
		next := i + 1
		if next == n {
			next = fxNone
		}
		dirs[i] = fxDir{0, fxNone, next, i}
		files[i] = fxFile{1, fxNone, i}
		ids[i] = uint64(i)
	}
	d := fxUtoc(ids, nil, fxIndex("", dirs, files, []string{strings.Repeat("d", 1000), "f.uasset"}))
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	within(t, 5*time.Second, "parseUtoc (long names)", func() { parseUtoc(d) })
	runtime.ReadMemStats(&after)
	if a := after.TotalAlloc - before.TotalAlloc; a > 128<<20 {
		t.Errorf("long names: %d bytes allocated", a)
	}
}

// fuzz-ish: random damage to a good file never hangs or panics.
func TestUtocRandomDamage(t *testing.T) {
	good := fxGoodUtoc()
	// the folder and file entries: 4-byte fields from here on
	links := len(good) - int(binary.LittleEndian.Uint32(good[48:])) + len(fxStr("../../../Game/Content/")) + 4
	rng := rand.New(rand.NewSource(8))
	special := []uint32{0, 1, 2, 3, 0x7FFFFFFF, 0x80000000, 0xFFFFFFFE, 0xFFFFFFFF, 1 << 16, 1<<16 + 1, uint32(len(good)), 1 << 20, 1 << 22}
	within(t, 20*time.Second, "parseUtoc on damaged files", func() {
		for n := 0; n <= len(good); n++ {
			parseUtoc(good[:n])
		}
		for i := 0; i < 30000; i++ {
			d := append([]byte(nil), good...)
			for k := 1 + rng.Intn(4); k > 0; k-- {
				at := rng.Intn(len(d) - 3)
				switch rng.Intn(4) {
				case 0:
					d[at] = byte(rng.Intn(256))
				case 1:
					binary.LittleEndian.PutUint32(d[at:], special[rng.Intn(len(special))])
				case 2:
					// a folder or file linking to another entry: loops, shared children
					binary.LittleEndian.PutUint32(d[links+4*rng.Intn(22):], uint32(rng.Intn(4)))
				default:
					at &^= 3
					binary.LittleEndian.PutUint32(d[at:], rng.Uint32())
				}
			}
			if rng.Intn(4) == 0 {
				d = d[:rng.Intn(len(d))]
			}
			if u, err := parseUtoc(d); err == nil {
				u.assetNames()
				u.packageIDs()
			}
		}
	})
}

// go test -fuzz=FuzzParseUtoc runs this for as long as wanted; a plain go test
// runs the seeds.
func FuzzParseUtoc(f *testing.F) {
	f.Add(fxGoodUtoc())
	f.Add(fxUtoc(nil, nil, fxIndex("", []fxDir{{fxNone, 1, fxNone, fxNone}, {0, 1, 1, fxNone}}, nil, []string{"a"})))
	f.Add(fxHeader(3, 2, 0xFFFFFFB7, 0xFFFFFFFF, 0xFFFFFFFF, 2, 0x8))
	f.Fuzz(func(t *testing.T, d []byte) {
		if u, err := parseUtoc(d); err == nil {
			u.assetNames()
		}
	})
}

// ---- the Mod Loader's log ----------------------------------------------------------

// runlog-1: a ~mods *_P.pak the game loaded itself is loaded, and counted.
func TestRunLogNativeTildePak(t *testing.T) {
	ri := parseRunLog("AC8 mod loader 1.3.1\r\n" +
		"hook installed, waiting for the game to mount pakchunk0-Windows.utoc\r\n" +
		"mods found=1 (loader folder 0, ~mods 1)\r\n" +
		"loadorder.txt=found\r\n" +
		"  on   ~mods\\Font_P.pak\r\n" +
		"  skipped Pak E:\\G\\Game\\Content\\Paks\\~mods\\Font_P.pak: already present in the native mount table\r\n" +
		"done: 0 of 0 attempted mount requests returned success (1 planned, 1 mod(s))\r\n")
	if r := ri.Mods[`~mods\font_p.pak`]; r == nil || r.Loaded != 1 || r.Failed != 0 {
		t.Fatalf("%+v", ri.Mods)
	}
	if ri.State != "ok" || ri.Mounted != 1 || ri.Total != 1 || ri.Summary != "1 of 1 mod files loaded." || len(ri.Problems) != 0 {
		t.Fatalf("%s %q %d/%d %q", ri.State, ri.Summary, ri.Mounted, ri.Total, ri.Problems)
	}
	// a ~mods set the game mounted (its .utoc came first), so the plan has nothing left
	ri = parseRunLog("AC8 mod loader 1.3.1\r\nhook installed, waiting\r\n" +
		"~mods folder=../../../Game/Content/Paks\\~mods\r\n" +
		"  skipped ../../../Game/Content/Paks\\~mods\\Trio\\Trio_P.pak: the game has already mounted a container with this name\r\n" +
		"nothing to mount\r\n")
	if r := ri.Mods[`~mods\trio`]; r == nil || r.Loaded != 1 || ri.State != "ok" || ri.Summary != "1 of 1 mod files loaded." {
		t.Fatalf("%s %q %+v", ri.State, ri.Summary, ri.Mods)
	}
	if ri = parseRunLog("AC8 mod loader 1.3.1\r\nhook installed\r\nnothing to mount\r\n"); ri.State != "nothing" {
		t.Fatalf("%s %q", ri.State, ri.Summary)
	}
}

const fxNativeLog = "AC8 mod loader 1.3.1\r\n" +
	"hook installed, waiting for the game to mount pakchunk0-Windows.utoc\r\n" +
	"stock mount code=0 order=103 path=../../../Game/Content/Paks/~mods/Trio_P.utoc\r\n" +
	"mods found=3 (loader folder 1, ~mods 2)\r\n" +
	"loadorder.txt=found\r\n" +
	"  skipped E:\\G\\Game\\Content\\Paks\\~mods\\Trio_P.pak: the game has already mounted a container with this name\r\n" +
	"mounting [Sky] order=1000 path=E:\\G\\IoStoreLoaderMod\\Sky\\Sky.utoc\r\n" +
	"  result code=0 message=OK\r\n" +
	"  skipped Pak E:\\G\\Game\\Content\\Paks\\~mods\\Font_P.pak: already present in the native mount table\r\n" +
	"done: 1 of 1 attempted mount requests returned success (3 planned, 3 mod(s))\r\n"

// runlog-1 in the state: each ~mods mod shows as Loaded, and the summary counts them.
func TestRunLogNativeTildeState(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	write(t, join(g.loaderDir(), "Sky", "Sky.utoc"), "t")
	write(t, join(g.loaderDir(), "Sky", "Sky.ucas"), "c")
	write(t, join(g.tildeModsDir(), "Font_P.pak"), "p")
	container(t, g.tildeModsDir(), "Trio_P")
	write(t, join(g.loaderDir(), "AC8IoStoreLoader.log"), fxNativeLog)
	st := buildState()
	if st.Run.State != "ok" || st.Run.Mounted != 3 || st.Run.Total != 3 {
		t.Fatalf("%s %q", st.Run.State, st.Run.Summary)
	}
	for _, m := range st.PakMods {
		if m.LastRun == nil || m.LastRun.Loaded != 1 {
			t.Errorf("%s: %+v", m.Key, m.LastRun)
		}
	}
}

// runlog-2: when the Mod Loader can't load .pak files on this game version,
// the start has problems, the summary says so, and each .pak mod says why.
func TestRunLogPakUnavailable(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	write(t, join(g.loaderDir(), "Sky", "Sky.utoc"), "t")
	write(t, join(g.loaderDir(), "Sky", "Sky.ucas"), "c")
	container(t, join(g.loaderDir(), "Font"), "Font_P")
	write(t, join(g.loaderDir(), "AC8IoStoreLoader.log"), "AC8 mod loader 1.3.1\r\n"+
		"Legacy Pak active mounting unavailable: unique entry/native IoStore call not verified; UTOC loading retained\r\n"+
		"hook installed, waiting for the game to mount pakchunk0-Windows.utoc\r\n"+
		"mounting [Sky] order=1001 path=E:\\G\\IoStoreLoaderMod\\Sky\\Sky.utoc\r\n"+
		"  result code=0 message=OK\r\n"+
		"  skipped Pak E:\\G\\IoStoreLoaderMod\\Font\\Font_P.pak: verified Legacy Mount or native platformFile unavailable\r\n"+
		"done: 1 of 1 attempted mount requests returned success (2 planned, 2 mod(s))\r\n")
	st := buildState()
	r := st.Run
	if r.State != "problems" || r.Mounted != 1 || r.Total != 2 || !strings.Contains(r.Summary, "can't load .pak mods on this game version") {
		t.Fatalf("%s %q %d/%d", r.State, r.Summary, r.Mounted, r.Total)
	}
	if len(r.Problems) != 1 || !strings.HasPrefix(r.Problems[0], "1 .pak mod file was not loaded") {
		t.Fatalf("%q", r.Problems)
	}
	for _, m := range st.PakMods {
		if m.Key == "Font" && (m.LastRun == nil || m.LastRun.Failed != 1 || !strings.Contains(m.LastRun.Messages[0], "game version")) {
			t.Fatalf("Font: %+v", m.LastRun)
		}
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"problems":[`) {
		t.Fatalf("problems not sent to the window: %s", b)
	}
}

// runlog-2: a .pak put off until the game was ready, then mounted after all, is
// no problem; one never mounted is. A skip repeated by the second pass counts once.
func TestRunLogDeferredPaks(t *testing.T) {
	head := "AC8 mod loader 1.3.1\r\nmod root=E:\\G\\IoStoreLoaderMod\r\nhook installed, waiting\r\n" +
		"  skipped E:\\G\\IoStoreLoaderMod\\Sky\\Half.utoc: its .ucas file is missing (Half.ucas)\r\n" +
		"  skipped Pak E:\\G\\IoStoreLoaderMod\\Font\\Font_P.pak: verified Legacy Mount or native platformFile unavailable\r\n" +
		"mounting [Sky] order=1001 path=E:\\G\\IoStoreLoaderMod\\Sky\\Sky.utoc\r\n" +
		"  result code=0 message=OK\r\n" +
		"done: 1 of 1 attempted mount requests returned success (2 planned, 2 mod(s))\r\n" +
		"Legacy Pak: deferred until a verified native platformFile is captured; UTOC results retained\r\n"
	ri := parseRunLog(head +
		"Legacy Pak: native object is now available; retrying deferred Pak plan only\r\n" +
		"  skipped E:\\G\\IoStoreLoaderMod\\Sky\\Half.utoc: its .ucas file is missing (Half.ucas)\r\n" +
		"mounting Legacy Pak [Font] order=1000 path=E:\\G\\IoStoreLoaderMod\\Font\\Font_P.pak\r\n" +
		"  Legacy result AL=1 pak-in-table=1 (paired UTOC registration is owned by the engine)\r\n" +
		"done: 1 of 1 attempted mount requests returned success (1 planned, 1 mod(s))\r\n")
	if ri.Mounted != 2 || ri.Total != 3 || ri.State != "problems" || len(ri.Problems) != 1 || !strings.Contains(ri.Problems[0], `Sky\Half.utoc was not loaded: part of the mod is missing (Half.ucas)`) {
		t.Fatalf("%s %q %d/%d %q", ri.State, ri.Summary, ri.Mounted, ri.Total, ri.Problems)
	}
	if r := ri.Mods["font"]; r == nil || r.Loaded != 1 || r.Failed != 0 {
		t.Fatalf("%+v", r)
	}
	ri = parseRunLog(head)
	if r := ri.Mods["font"]; r == nil || r.Failed != 1 || !strings.Contains(r.Messages[0], "Start the game again") || !strings.Contains(ri.Summary, "wasn't ready") {
		t.Fatalf("%q %+v", ri.Summary, r)
	}
}

// A .pak in the Mod Loader's folder "already present in the native mount
// table": the game had another file with that name, so this one did not load.
func TestRunLogNameTakenInLoaderFolder(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	write(t, join(g.loaderDir(), "Dup", "Dup_P.pak"), "p")
	ri := parseRunLog("AC8 mod loader 1.3.1\r\nmod root=E:\\G\\ue4ss\\Mods\\IoStoreLoaderMod\r\nhook installed\r\n" +
		"  skipped Pak E:\\G\\ue4ss\\Mods\\IoStoreLoaderMod\\Dup\\Dup_P.pak: already present in the native mount table\r\n" +
		"done: 0 of 0 attempted mount requests returned success (1 planned, 1 mod(s))\r\n")
	if ri.State != "problems" || ri.Total != 1 || len(ri.Problems) != 1 || !strings.HasPrefix(ri.Problems[0], `Dup\Dup_P.pak was not loaded`) {
		t.Fatalf("%s %q %q", ri.State, ri.Summary, ri.Problems)
	}
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	addModNotes(mods, ri, modeModded)
	if len(mods) != 1 || len(mods[0].Notes) != 1 || mods[0].Notes[0].Kind != "warn" || !strings.Contains(mods[0].Notes[0].Text, "another file named Dup_P.pak") {
		t.Fatalf("%+v", mods[0].Notes)
	}
}

// runlog-3: another program hooked only the game's .pak reader.
func TestRunLogPakHookTaken(t *testing.T) {
	ri := parseRunLog("AC8 mod loader 1.3.1\r\nmod root=E:\\x\r\nmount signature matches=1 address=0000000142\r\n" +
		"Pak constructor already hooked by another loader at 00007FF812340000; nothing changed\r\n")
	if ri.State != "otherloader" || !strings.Contains(ri.Summary, "remove the other one") {
		t.Fatalf("%s %q", ri.State, ri.Summary)
	}
}

// The texts lead with what it means and what to do.
func TestRunLogPlainWords(t *testing.T) {
	ri := parseRunLog("AC8 mod loader 1.1.0\nhook installed\nmounting [A] order=1000 path=x\n  result code=5 message=Corrupt toc\ndone: 0 of 1 containers mounted from 1 mod(s)\n")
	if m := ri.Mods["a"].Messages[0]; m != "The game couldn't read this mod. It may be damaged or made for another game version: download it again, or ask its author. (Corrupt toc)" {
		t.Fatalf("%q", m)
	}
}

// ---- pak mods ----------------------------------------------------------------------

// mods-1: a ~mods *_P.pak switched off still loads; the mod says so.
func TestTildePakSwitchNote(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	write(t, join(g.tildeModsDir(), "EnglishFonts_P.pak"), "p")
	if err := writeLoadOrder(g.loaderDir(), []orderEntry{{`~mods\EnglishFonts_P.pak`, false}}); err != nil {
		t.Fatal(err)
	}
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	addModNotes(mods, nil, modeModded)
	if len(mods) != 1 || mods[0].Enabled || len(mods[0].Notes) != 1 || mods[0].Notes[0].Kind != "warn" ||
		!strings.HasPrefix(mods[0].Notes[0].Text, "Still loads: the game loads .pak files in the ~mods folder by itself") || !strings.Contains(mods[0].Notes[0].Text, "use Remove") {
		t.Fatalf("%+v", mods[0].Notes)
	}
	writeLoadOrder(g.loaderDir(), []orderEntry{{`~mods\EnglishFonts_P.pak`, true}})
	mods, _ = scanPakMods(g.loaderDir(), g.tildeModsDir())
	addModNotes(mods, nil, modeModded)
	if len(mods[0].Notes) != 1 || mods[0].Notes[0].Kind != "info" || !strings.Contains(mods[0].Notes[0].Text, "don't change it") {
		t.Fatalf("%+v", mods[0].Notes)
	}
	// a .utoc-only mod in ~mods is the Mod Loader's: no such note
	write(t, join(g.tildeModsDir(), "Sky", "Sky.utoc"), "t")
	write(t, join(g.tildeModsDir(), "Sky", "Sky.ucas"), "c")
	mods, _ = scanPakMods(g.loaderDir(), g.tildeModsDir())
	addModNotes(mods, nil, modeModded)
	for _, m := range mods {
		if m.Key == `~mods\Sky` && len(m.Notes) != 0 {
			t.Fatalf("%+v", m.Notes)
		}
	}
}

// mods-1: the game loads ~mods .pak files before the Mod Loader's mods, so in
// a conflict the Mod Loader's mod wins wherever it is in the list; and a
// same-named file elsewhere is the one that doesn't load.
func TestTildePakConflicts(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	tm, ld := g.tildeModsDir(), g.loaderDir()
	pak := func(dir, base string, ids ...uint64) {
		write(t, join(dir, base+".utoc"), string(fxUtoc(ids, nil, nil)))
		write(t, join(dir, base+".ucas"), "c")
		write(t, join(dir, base+".pak"), "p")
	}
	pak(tm, "Font_P", 7, 8)
	pak(join(tm, "Other"), "Other_P", 8)
	write(t, join(ld, "Sky", "Sky.utoc"), string(fxUtoc([]uint64{7}, nil, nil)))
	write(t, join(ld, "Sky", "Sky.ucas"), "c")
	write(t, join(ld, "Copy", "Font_P.pak"), "p") // same name as the ~mods one, and newer
	old := time.Now().Add(-time.Hour)
	os.Chtimes(join(tm, "Font_P.pak"), old, old)
	writeLoadOrder(ld, []orderEntry{{"Sky", true}, {"Copy", true}, {`~mods\Font_P.pak`, true}, {`~mods\Other`, true}})
	mods, _ := scanPakMods(ld, tm)
	addModNotes(mods, nil, modeModded)
	notes := map[string][]Note{}
	for _, m := range mods {
		notes[m.Key] = m.Notes
	}
	has := func(key, kind, text string) {
		t.Helper()
		for _, n := range notes[key] {
			if n.Kind == kind && strings.Contains(n.Text, text) {
				return
			}
		}
		t.Errorf("%s: no %s note with %q in %+v", key, kind, text, notes[key])
	}
	has("Sky", "info", "1 game file is also changed by ~mods\\Font_P.pak. This mod's version is the one you see")
	has(`~mods\Font_P.pak`, "warn", "1 game file is also changed by Sky. That mod's version is the one you see")
	has(`~mods\Font_P.pak`, "info", "The game loads both from ~mods by itself")
	has(`~mods\Other`, "info", "The game loads both from ~mods by itself")
	has("Copy", "warn", "Won't load: the game already loads a file with the same name (Font_P.pak) from ~mods\\Font_P.pak by itself")
}

// mods-2: a loose skin list is the skin list too: it is moved last once, and
// loadorder.txt is not written again and again.
func TestLooseSkinListNoLoop(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	ld := g.loaderDir()
	write(t, join(ld, skinListName+".utoc"), "t")
	write(t, join(ld, skinListName+".ucas"), "c")
	write(t, join(g.tildeModsDir(), "Other", "Other.utoc"), "t")
	write(t, join(g.tildeModsDir(), "Other", "Other.ucas"), "c")
	writeLoadOrder(ld, []orderEntry{{skinListName + ".utoc", true}, {`~mods\Other`, true}})
	st := buildState()
	if got := keys(st.PakMods); !reflect.DeepEqual(got, []string{`~mods\Other`, skinListName + ".utoc"}) || skinListNotLast(st.PakMods) || !st.PakMods[1].SkinList {
		t.Fatalf("%q", got)
	}
	p := join(ld, "loadorder.txt")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	os.Chtimes(p, old, old)
	buildState()
	if s, _ := os.Stat(p); !s.ModTime().Equal(old) {
		t.Fatalf("loadorder.txt written again: %v", s.ModTime())
	}
	for key, want := range map[string]bool{skinListName: true, `~mods\` + skinListName: true, skinListName + ".utoc": true,
		`~mods\zz_skinstudio_skinlist_P.pak`: true, skinListName + "_P.utoc": true, "zz_SkinStudio_SkinList v2": false, "Other": false, `~mods\`: false} {
		if isSkinListKey(key) != want {
			t.Errorf("isSkinListKey(%q) = %v", key, !want)
		}
	}
}

// writeLoadOrder leaves the file alone when nothing changes.
func TestWriteLoadOrderUnchanged(t *testing.T) {
	ld := t.TempDir()
	e := []orderEntry{{"A", true}, {"B", false}}
	writeLoadOrder(ld, e)
	p := join(ld, "loadorder.txt")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	os.Chtimes(p, old, old)
	writeLoadOrder(ld, e)
	if s, _ := os.Stat(p); !s.ModTime().Equal(old) {
		t.Fatal("rewritten")
	}
	writeLoadOrder(ld, []orderEntry{{"B", false}, {"A", true}})
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "-B\r\n+A\r\n") {
		t.Fatalf("%s", b)
	}
}

// mods-4: one shared game file is one, however many of a mod's files carry
// it, and the notes come in the same order every time.
func TestConflictCountAndOrder(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	ld := g.loaderDir()
	for p, ids := range map[string][]uint64{"A/1K/A1": {7}, "A/2K/A2": {7}, "B/B": {7, 9}, "C/C": {7, 9}, "D/D": {9}} {
		write(t, join(ld, p+".utoc"), string(fxUtoc(ids, nil, nil)))
		write(t, join(ld, p+".ucas"), "c")
	}
	var first [][]Note
	for i := 0; i < 15; i++ {
		mods, _ := scanPakMods(ld, g.tildeModsDir())
		addModNotes(mods, nil, modeModded)
		var all [][]Note
		for _, m := range mods {
			all = append(all, m.Notes)
		}
		if i == 0 {
			first = all
			if mods[0].Key != "A" || len(mods[0].Notes) != 2 || mods[0].Notes[0].Text != "1 game file is also changed by B. That mod loads later, so its version is the one you see." {
				t.Fatalf("%+v", mods[0].Notes)
			}
			if mods[2].Key != "C" || mods[2].Notes[0].Text != "1 game file is also changed by D. That mod loads later, so its version is the one you see." ||
				mods[2].Notes[1].Text != "1 game file is also changed by A. This mod loads later, so its version is the one you see." ||
				mods[2].Notes[2].Text != "2 game files are also changed by B. This mod loads later, so its version is the one you see." {
				t.Fatalf("%+v", mods[2].Notes)
			}
		} else if !reflect.DeepEqual(all, first) {
			t.Fatalf("notes changed order:\n%+v\n%+v", first, all)
		}
	}
}

// mods-3: a folder whose name starts with a space can't be switched off; say so.
func TestLeadingSpaceNote(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	write(t, join(g.loaderDir(), " Mod", "x.utoc"), "t")
	write(t, join(g.loaderDir(), " Mod", "x.ucas"), "c")
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	addModNotes(mods, nil, modeModded)
	if len(mods) != 1 || len(mods[0].Notes) != 1 || mods[0].Notes[0].Kind != "warn" || !strings.Contains(mods[0].Notes[0].Text, "name starts with a space") {
		t.Fatalf("%+v", mods[0].Notes)
	}
}

// mods-5: Windows, and the game under Wine on Linux, find files in any case.
func TestFileNamesInOtherCase(t *testing.T) {
	g := newGame(t)
	installLoader131(t, g)
	ld := g.loaderDir()
	write(t, join(ld, "Plane", "Plane_P.utoc"), "t")
	write(t, join(ld, "Plane", "Plane_P.UCAS"), "cc")
	write(t, join(ld, "X_P.PAK"), "p")
	write(t, join(ld, "X_P.utoc"), "t")
	write(t, join(ld, "X_P.ucas"), "c")
	mods, _ := scanPakMods(ld, g.tildeModsDir())
	if got := keys(mods); !reflect.DeepEqual(got, []string{"Plane", "X_P.PAK"}) {
		t.Fatalf("%q", got)
	}
	for _, m := range mods {
		if c := m.Containers[0]; c.Skip != "" || m.Loads != 1 {
			t.Errorf("%s: %q", m.Key, c.Skip)
		}
	}
	if mods[0].Size != 3 {
		t.Errorf("size %d", mods[0].Size)
	}
	md := join(g.ue4ssRoot(), "Mods")
	write(t, join(md, "LowerScripts", "scripts", "main.lua"), "-- lower case folder\n")
	write(t, join(md, "LowerScripts", "enabled.txt"), "")
	write(t, join(md, "Toggle", "Scripts", "main.lua"), "")
	write(t, join(md, "Toggle", "Enabled.TXT"), "")
	state := map[string]bool{}
	for _, m := range listScriptMods(g.ue4ssRoot()) {
		state[m.Name] = m.Enabled
		if m.Name == "LowerScripts" && m.Description != "lower case folder" {
			t.Errorf("%+v", m)
		}
	}
	if !state["LowerScripts"] || !state["Toggle"] {
		t.Fatalf("%v", state)
	}
	if err := setScriptMod(g.ue4ssRoot(), "Toggle", false); err != nil {
		t.Fatal(err)
	}
	if isFileFold(join(md, "Toggle"), "enabled.txt") || !isFileFold(join(md, "Toggle"), "enabled.off") {
		t.Fatal("not switched off")
	}
	if err := setScriptMod(g.ue4ssRoot(), "Toggle", true); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(join(md, "Toggle"))
	n := 0
	for _, e := range ents {
		if strings.HasPrefix(strings.ToLower(e.Name()), "enabled.") {
			n++
		}
	}
	if n != 1 || !isFileFold(join(md, "Toggle"), "enabled.txt") {
		t.Fatalf("%d enabled files", n)
	}
}

// ---- script mods -------------------------------------------------------------------

// scripts-1: on and off the way UE4SS 3.0.1 reads them.
func TestScriptModsLikeUE4SS(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	md := join(g.ue4ssRoot(), "Mods")
	for _, n := range []string{"OnByTxt", "Sky Live", "SkyLive", "Lower", "Commented", "Tight"} {
		write(t, join(md, n, "Scripts", "main.lua"), "")
	}
	write(t, join(md, "OnByTxt", "enabled.txt"), "")
	write(t, join(md, "mods.txt"), "IoStoreLoaderMod : 1\r\nOnByTxt : 0\r\nSky Live : 1\r\nlower : 1\r\nCommented : 1 ; on\r\nTight:1\r\n; Built-in keybinds, do not move up!\r\nKeybinds : 1\r\n")
	state := map[string]bool{}
	for _, m := range listScriptMods(g.ue4ssRoot()) {
		state[m.Name] = m.Enabled
	}
	want := map[string]bool{
		"OnByTxt":   true,  // enabled.txt starts it whatever mods.txt says
		"Sky Live":  false, // UE4SS reads "SkyLive : 1", and the folder is "Sky Live"
		"SkyLive":   true,  // ...which is this one
		"Lower":     false, // names match exactly
		"Commented": false, // lines with ';' are skipped
		"Tight":     true,
	}
	if !reflect.DeepEqual(state, want) {
		t.Fatalf("%v", state)
	}
	if !modsTxtState(md)["iostoreloadermod"] || !g.setupStatus().LoaderOn {
		t.Fatal("IoStoreLoaderMod")
	}
	for in, out := range map[string]string{"AC8.Stonehenge": "AC8", "RoGLaser v1.2": "RoGLaser v1", ".hidden": ".hidden", "Plain": "Plain"} {
		if got := ue4ssName(in); got != out {
			t.Errorf("ue4ssName(%q) = %q", in, got)
		}
	}
	b, _ := json.Marshal(listScriptMods(g.ue4ssRoot())[0])
	if !strings.Contains(string(b), `"mtime":"`) || !strings.Contains(string(b), `"ue4ssName":"Commented"`) {
		t.Fatalf("%s", b)
	}
}

// ue4sslog-1: UE4SS names "AC8.Stonehenge" "AC8"; the window looks the folder up.
func TestScriptRunDottedFolder(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	md := join(g.ue4ssRoot(), "Mods")
	write(t, join(md, "AC8.Stonehenge", "dlls", "main.dll"), "MZ")
	write(t, join(md, "AC8.Stonehenge", "enabled.txt"), "")
	write(t, join(g.ue4ssRoot(), "UE4SS.log"), "[x] Starting mods (from enabled.txt (E:\\M), no defined load order)...\n[x] Mod 'AC8' has enabled.txt, starting mod.\n")
	st := buildState()
	if st.ScriptRun == nil || !st.ScriptRun.Started["ac8.stonehenge"] || len(st.ScriptRun.Problems) != 0 {
		t.Fatalf("%+v", st.ScriptRun)
	}
	// a Lua mod with the same name is a different mod to UE4SS
	write(t, join(md, "AC8", "Scripts", "main.lua"), "")
	if st = buildState(); len(st.ScriptRun.Problems) != 0 {
		t.Fatalf("%q", st.ScriptRun.Problems)
	}
	// two C++ mods UE4SS sees as one
	write(t, join(md, "AC8", "dlls", "main.dll"), "MZ")
	st = buildState()
	if p := st.ScriptRun.Problems; len(p) != 1 || !strings.HasPrefix(p[0], "Only one of AC8 and AC8.Stonehenge can run") {
		t.Fatalf("%q", p)
	}
}

// ue4sslog-2: script errors in both UE4SS formats, said in plain words.
func TestScriptRunErrors(t *testing.T) {
	sr := parseScriptRun("[x] Mod 'SkyLive' has enabled.txt, starting mod.\n" +
		"[x] Error loading script: @E:\\Games\\ACE COMBAT 8\\Game\\Binaries\\Win64\\ue4ss\\Mods\\SkyLive\\Scripts\\main.lua:3: unexpected symbol near 'end'\n" +
		"[x] Failed to execute main script: E:\\Games\\ACE COMBAT 8\\Game\\Binaries\\Win64\\ue4ss\\Mods\\SkyLive\\Scripts\\main.lua\n" +
		"[x] Mod 'Other' has enabled.txt, starting mod.\n" +
		"[x] Error executing script: E:\\ue4ss\\Mods\\Other\\Scripts\\main.lua:5: attempt to index a nil value (global 'x')\n" +
		"[x] [Lua::execute_file] luaL_loadfile returned E:\\ue4ss\\Mods\\Old\\Scripts\\main.lua:3: unexpected symbol\n" +
		"[x] Main script 'main.lua' not found in scripts directory: E:\\ue4ss\\Mods\\Caps\\Scripts -- Ensure your script file uses the correct casing.\n" +
		"[x] Failed to load dll <E:\\ue4ss\\Mods\\RoG\\dlls\\main.dll> for mod RoG, error code: 0x7e\n")
	if sr == nil || len(sr.Problems) != 5 {
		t.Fatalf("%+v", sr)
	}
	want := []string{
		"SkyLive has an error in its script, so it may not work. Download it again, or tell its author. (Error loading script: SkyLive\\Scripts\\main.lua:3: unexpected symbol near 'end')",
		"Other has an error in its script",
		"Old has an error in its script",
		"Caps can't start: UE4SS found no Scripts\\main.lua in it",
		"RoG can't start: UE4SS couldn't load its main.dll",
	}
	for i, w := range want {
		if !strings.HasPrefix(sr.Problems[i], w) {
			t.Errorf("problem %d: %q\nwant %q", i, sr.Problems[i], w)
		}
	}
}

// ue4sslog-3: a name with a quote in it is read to the end.
func TestScriptRunQuoteInName(t *testing.T) {
	sr := parseScriptRun("[x] Starting Lua mod 'Mobius1's_HUD'\r\n[x] Starting C++ mod 'Mobius' Skin'\n")
	if sr == nil || !sr.Started["mobius1's_hud"] || !sr.Started["mobius' skin"] || len(sr.Started) != 2 {
		t.Fatalf("%+v", sr)
	}
}
