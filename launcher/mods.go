package main

// Pak mods (.utoc/.ucas, and *_P.pak from Mod Loader 1.3.0 on), found and
// ordered by exactly the rules the installed Mod Loader (dlls\main.dll) uses,
// so what the launcher shows is what the game loads.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
)

const maxSearchDepth = 6

// foldPath finds dir\parts... the way Windows does, and Wine for the game on
// Linux: exact first, then ignoring upper and lower case. "" when not there.
// On Windows the exact look-up already ignores case, so nothing more is done.
func foldPath(dir string, parts ...string) string { return folderNames(nil).foldPath(dir, parts...) }

// folderNames remembers, for one scan, the names in each folder foldPath had
// to read, so a folder of many mods is read once and not once for each file
// looked for in it. A nil one remembers nothing.
type folderNames map[string]map[string]string

func (fn folderNames) foldPath(dir string, parts ...string) string {
	p := dir
	for _, part := range parts {
		q := join(p, part)
		if _, err := os.Stat(q); err == nil {
			p = q
			continue
		}
		if runtime.GOOS == "windows" {
			return ""
		}
		found := fn.find(p, part)
		if found == "" {
			return ""
		}
		p = join(p, found)
	}
	return p
}

// find is the first name in dir (A to Z) that matches name in any case, or "".
func (fn folderNames) find(dir, name string) string {
	if fn == nil {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return ""
		}
		for _, e := range ents {
			if strings.EqualFold(e.Name(), name) {
				return e.Name()
			}
		}
		return ""
	}
	names, ok := fn[dir]
	if !ok {
		names = map[string]string{}
		if ents, err := os.ReadDir(dir); err == nil {
			for _, e := range ents {
				if k := foldKey(e.Name()); names[k] == "" {
					names[k] = e.Name()
				}
			}
		}
		fn[dir] = names
	}
	return names[foldKey(name)]
}

// foldKey is the same for two names exactly when strings.EqualFold matches
// them: each letter becomes the smallest of its upper and lower case forms.
func foldKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		low := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			low = min(low, f)
		}
		b.WriteRune(low)
	}
	return b.String()
}

// isFileFold: a file at dir\parts..., in any case (see foldPath).
func isFileFold(dir string, parts ...string) bool {
	p := foldPath(dir, parts...)
	return p != "" && isFile(p)
}

// sibling is the file next to p with the same name and extension ext, in any
// case (see foldPath); "" when there is none.
func sibling(p, ext string) string { return folderNames(nil).sibling(p, ext) }

func (fn folderNames) sibling(p, ext string) string {
	dir, name := filepath.Split(p)
	s := fn.foldPath(filepath.Clean(dir), strings.TrimSuffix(name, filepath.Ext(name))+ext)
	if s == "" || !isFile(s) {
		return ""
	}
	return s
}

var skipDirs = []string{"dlls", "scripts", "source", "build", "packed", "export",
	"backup", "backups", "old", "disabled", "removed-mods"}

func isSkippedDir(name string) bool {
	for _, s := range skipDirs {
		if strings.EqualFold(name, s) {
			return true
		}
	}
	return false
}

// foldCmp compares like the loader: ASCII a-z folded to upper case, nothing else.
func foldCmp(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	for i := 0; ; i++ {
		if i >= len(ra) || i >= len(rb) {
			return len(ra) - len(rb)
		}
		x, y := ra[i], rb[i]
		if x >= 'a' && x <= 'z' {
			x -= 32
		}
		if y >= 'a' && y <= 'z' {
			y -= 32
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
}

type dirEntry struct {
	name   string
	isDir  bool
	isLink bool
	mtime  time.Time
	size   int64
}

// isPakName: a *_P.pak file, the only .pak files Mod Loader 1.3 loads.
func isPakName(name string) bool { return strings.HasSuffix(strings.ToLower(name), "_p.pak") }

// isPakSidecar: a *_P.utoc next to a .pak of the same name. It belongs to the
// .pak (the game mounts it with the .pak), so it is not a mod or container of its own.
func isPakSidecar(path string) bool { return folderNames(nil).isPakSidecar(path) }

func (fn folderNames) isPakSidecar(path string) bool {
	if !strings.HasSuffix(strings.ToLower(path), "_p.utoc") {
		return false
	}
	return fn.sibling(path, ".pak") != ""
}

// containerKey is the name two containers clash on: the .utoc name (a .pak and
// its .utoc share one).
func containerKey(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)) + ".utoc")
}

// loaderPaks reports whether the Mod Loader in loaderDir loads *_P.pak files
// (1.3.0 and newer). Remembered per DLL date, so the DLL is read once.
var (
	paksMu    sync.Mutex
	paksCache = map[string]struct {
		mtime time.Time
		yes   bool
	}{}
)

func loaderPaks(loaderDir string) bool {
	dll := join(loaderDir, "dlls", "main.dll")
	st, err := os.Stat(dll)
	if err != nil {
		return false
	}
	paksMu.Lock()
	defer paksMu.Unlock()
	if c, ok := paksCache[dll]; ok && c.mtime.Equal(st.ModTime()) {
		return c.yes
	}
	v := loaderVersion(dll)
	yes := v != "" && v != "unknown" && !versionLess(v, "1.3.0")
	paksCache[dll] = struct {
		mtime time.Time
		yes   bool
	}{st.ModTime(), yes}
	return yes
}

// listDir returns the folders, .utoc files and (with paks) *_P.pak files
// directly inside dir, A to Z.
func listDir(dir string, paks bool) []dirEntry {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []dirEntry
	for _, e := range ents {
		name := e.Name()
		lst, err := os.Lstat(join(dir, name))
		if err != nil {
			continue
		}
		link := lst.Mode()&os.ModeSymlink != 0 || lst.Mode()&os.ModeIrregular != 0
		st := lst
		if link {
			if s2, err := os.Stat(join(dir, name)); err == nil {
				st = s2
			}
		}
		if !st.IsDir() && !strings.HasSuffix(strings.ToLower(name), ".utoc") && !(paks && isPakName(name)) {
			continue
		}
		out = append(out, dirEntry{name: name, isDir: st.IsDir(), isLink: link, mtime: st.ModTime(), size: st.Size()})
	}
	sort.SliceStable(out, func(i, j int) bool { return foldCmp(out[i].name, out[j].name) < 0 })
	return out
}

func hasUtoc(dir string, depth int, paks bool) bool {
	for _, e := range listDir(dir, paks) {
		if !e.isDir {
			return true
		}
		if depth < maxSearchDepth && !e.isLink && !isSkippedDir(e.name) && hasUtoc(join(dir, e.name), depth+1, paks) {
			return true
		}
	}
	return false
}

type Container struct {
	Path  string    `json:"path"`
	Name  string    `json:"name"`
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
	Skip  string    `json:"skip,omitempty"` // why the loader will not mount it
	Files []string  `json:"files,omitempty"`
	ids   map[uint64]bool
	// native: a *_P.pak in ~mods. The game mounts those itself (with the Mod
	// Loader's .pak hook in place), whatever loadorder.txt says, and before the
	// Mod Loader mounts its mods.
	native bool
}

func (fn folderNames) collectUtoc(dir string, depth int, paks bool, out *[]*Container) {
	ents := listDir(dir, paks)
	for pass := 0; pass < 2; pass++ {
		for _, e := range ents {
			if e.isDir != (pass == 1) {
				continue
			}
			p := join(dir, e.name)
			if !e.isDir {
				if !(paks && fn.isPakSidecar(p)) {
					*out = append(*out, &Container{Path: p, Name: e.name, Mtime: e.mtime})
				}
			} else if depth < maxSearchDepth && !e.isLink && !isSkippedDir(e.name) {
				fn.collectUtoc(p, depth+1, paks, out)
			}
		}
	}
}

type SkinInfo struct {
	Title    string   `json:"title"`
	Aircraft []string `json:"aircraft"`
	MadeWith string   `json:"madeWith"`
}

type Note struct {
	Kind string `json:"kind"` // "warn" or "info"
	Text string `json:"text"`
}

type ModRun struct {
	Loaded   int      `json:"loaded"`
	Failed   int      `json:"failed"`
	Messages []string `json:"messages,omitempty"`
}

type PakMod struct {
	Key        string       `json:"key"` // the name used in loadorder.txt
	Name       string       `json:"name"`
	Where      string       `json:"where"` // "loader" or "tilde"
	Path       string       `json:"path"`
	IsFile     bool         `json:"isFile"`
	Enabled    bool         `json:"enabled"`
	Containers []*Container `json:"containers"`
	Size       int64        `json:"size"`
	Mtime      time.Time    `json:"mtime"`
	Notes      []Note       `json:"notes"`
	Skin       *SkinInfo    `json:"skin,omitempty"`
	SkinList   bool         `json:"skinList"`
	LastRun    *ModRun      `json:"lastRun,omitempty"`
	Loads      int          `json:"loads"`             // containers the loader will mount next start
	Added      *time.Time   `json:"added,omitempty"`   // added through the launcher
	Updated    *time.Time   `json:"updated,omitempty"` // last updated through the launcher
	// inStorage: a ~mods mod while the ~mods folder is in the mod storage
	// folder (Unmodded), where the game doesn't see it
	inStorage bool
}

func (fn folderNames) findMods(dir, prefix, where string, paks bool) []*PakMod {
	var mods []*PakMod
	for _, e := range listDir(dir, paks) {
		p := join(dir, e.name)
		if !e.isDir && paks && fn.isPakSidecar(p) {
			continue
		}
		if e.isDir && (isSkippedDir(e.name) || !hasUtoc(p, 1, paks)) {
			continue
		}
		m := &PakMod{Key: prefix + e.name, Name: e.name, Where: where, Path: p, IsFile: !e.isDir, Enabled: true}
		if m.IsFile {
			m.Name = strings.TrimSuffix(e.name, filepath.Ext(e.name))
			m.Containers = []*Container{{Path: p, Name: e.name, Mtime: e.mtime}}
		} else {
			fn.collectUtoc(p, 1, paks, &m.Containers)
		}
		mods = append(mods, m)
	}
	return mods
}

const skinListName = "zz_SkinStudio_SkinList"

// scanPakMods finds every mod, in the loader's order, with loadorder.txt applied.
func scanPakMods(loaderDir, tildeDir string) (mods []*PakMod, staleEntries []string) {
	paks := loaderPaks(loaderDir)
	fn := folderNames{}
	found := fn.findMods(loaderDir, "", "loader", paks)
	tilde := fn.findMods(tildeDir, `~mods\`, "tilde", paks)
	if len(tilde) > 0 && !tildeInGame(tildeDir) {
		for _, m := range tilde {
			m.inStorage = true
		}
	}
	found = append(found, tilde...)

	text, _ := os.ReadFile(join(loaderDir, "loadorder.txt"))
	placed := make([]bool, len(found))
	for _, raw := range strings.Split(decodeText(text), "\n") {
		line := strings.TrimLeft(strings.TrimRight(raw, "\r"), " \t\uFEFF")
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		enabled := true
		if line[0] == '+' || line[0] == '-' {
			enabled = line[0] == '+'
			line = line[1:]
		}
		name := strings.ReplaceAll(strings.Trim(line, " \t"), "/", `\`)
		if name == "" {
			continue
		}
		idx := -1
		for i, m := range found {
			if strings.EqualFold(m.Key, name) {
				idx = i
				break
			}
		}
		if idx < 0 {
			staleEntries = append(staleEntries, name)
			continue
		}
		if placed[idx] {
			continue
		}
		placed[idx] = true
		found[idx].Enabled = enabled
		mods = append(mods, found[idx])
	}
	for i, m := range found {
		if !placed[i] {
			mods = append(mods, m)
		}
	}
	for _, m := range mods {
		for _, c := range m.Containers {
			c.native = paks && m.Where == "tilde" && isPakName(c.Name)
		}
		fn.describeMod(m)
	}
	fn.checkContainers(mods)
	return mods, staleEntries
}

// tildeInGame: the ~mods folder dir is the game's own (Game\Content\Paks\~mods
// in the folder with AceCombat8.exe), not the copy in the mod storage folder.
func tildeInGame(dir string) bool {
	return looksLikeGame(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(dir)))))
}

// checkContainers marks what the loader will skip among the mods that are on:
// a second container with the same name (the newer copy wins; a .pak and its
// .utoc count as one name) and a .utoc without its .ucas. A .pak needs a .ucas
// only when it comes with a .utoc. A .pak the game loads from ~mods by itself
// is loaded first, also when it is switched off, so it wins a name clash.
func (fn folderNames) checkContainers(mods []*PakMod) {
	var plan []*Container
	owner := map[*Container]*PakMod{}
	for _, m := range mods {
		for _, c := range m.Containers {
			c.Skip = ""
			if m.Enabled || c.native {
				plan = append(plan, c)
				owner[c] = m
			}
		}
	}
	// the plan positions of the containers that share each name, in order:
	// only those are compared, not every pair in a folder of many files
	keys := make([]string, len(plan))
	byKey := map[string][]int{}
	for i, c := range plan {
		keys[i] = containerKey(c.Name)
		byKey[keys[i]] = append(byKey[keys[i]], i)
	}
	for i, c := range plan {
		if c.Skip != "" {
			continue
		}
		for _, j := range byKey[keys[i]] {
			d := plan[j]
			if j <= i || d.Skip != "" {
				continue
			}
			older, newer := d, c
			if d.Mtime.After(c.Mtime) {
				older, newer = c, d
			}
			switch {
			case c.native != d.native:
				older, newer = d, c
				if d.native {
					older, newer = c, d
				}
				older.Skip = "Won't load: the game already loads a file with the same name (" + newer.Name + ") from " + owner[newer].Key + " by itself, and only one can load. Remove the copy you don't want."
			default:
				older.Skip = "Won't load: " + owner[newer].Key + " has a newer copy of this file (" + newer.Name + "), and only one can load. Remove the copy you don't want."
			}
			if older == c {
				break
			}
		}
	}
	for _, c := range plan {
		if isPakName(c.Name) && fn.sibling(c.Path, ".utoc") == "" {
			continue // a .pak on its own
		}
		if c.Skip == "" && fn.sibling(c.Path, ".ucas") == "" {
			base := strings.TrimSuffix(c.Name, filepath.Ext(c.Name))
			c.Skip = "Won't load: part of this mod is missing (" + base + ".ucas). Download it again, or ask its author for the full files."
		}
	}
	for _, m := range mods {
		m.Loads = 0
		for _, c := range m.Containers {
			if m.Enabled && c.Skip == "" {
				m.Loads++
			}
		}
	}
}

// describeMod fills in sizes, the files each container changes and Skin Studio details.
func (fn folderNames) describeMod(m *PakMod) {
	for _, c := range m.Containers {
		for _, ext := range []string{".utoc", ".ucas", ".pak"} {
			if p := fn.sibling(c.Path, ext); p != "" {
				if st, err := os.Stat(p); err == nil {
					c.Size += st.Size()
				}
			}
		}
		m.Size += c.Size
		if c.Mtime.After(m.Mtime) {
			m.Mtime = c.Mtime
		}
		if p := fn.sibling(c.Path, ".utoc"); p != "" {
			if toc, err := readUtoc(p); err == nil {
				c.ids = toc.packageIDs()
				c.Files = toc.assetNames()
			}
		}
	}
	if isSkinListKey(m.Key) {
		m.SkinList = true
	}
	if !m.IsFile {
		if b, err := os.ReadFile(join(m.Path, "skinstudio.json")); err == nil {
			var j struct {
				Title    string `json:"title"`
				MadeWith string `json:"madeWith"`
				Slots    []struct {
					Plane string `json:"plane"`
				} `json:"slots"`
			}
			if json.Unmarshal(b, &j) == nil {
				si := &SkinInfo{Title: j.Title, MadeWith: j.MadeWith}
				seen := map[string]bool{}
				for _, s := range j.Slots {
					if s.Plane != "" && !seen[s.Plane] {
						seen[s.Plane] = true
						si.Aircraft = append(si.Aircraft, s.Plane)
					}
				}
				m.Skin = si
			}
		}
	}
}

// addModNotes adds the things a player should know about each mod: what the
// loader will skip, two mods that change the same game files, and how the mod
// did the last time the game started.
func addModNotes(mods []*PakMod, run *RunInfo, mode string) {
	// loaded: per package, each mod that changes it, and whether through a
	// container the Mod Loader mounts (true) or only one the game loads itself
	// from ~mods (false). A switched-off mod still counts for its ~mods .pak files.
	loaded := map[uint64]map[*PakMod]bool{}
	at := map[*PakMod]int{}
	for i, m := range mods {
		at[m] = i
		m.Notes = []Note{}
		for _, c := range m.Containers {
			if !m.Enabled && !c.native {
				continue
			}
			if c.Skip != "" {
				if m.Enabled {
					m.Notes = append(m.Notes, Note{"warn", c.Skip})
				}
				continue
			}
			for id := range c.ids {
				if loaded[id] == nil {
					loaded[id] = map[*PakMod]bool{}
				}
				loaded[id][m] = loaded[id][m] || !c.native
			}
		}
		if run != nil && m.Enabled {
			for _, n := range run.notLoaded[strings.ToLower(m.Key)] {
				shown := false
				for _, c := range m.Containers {
					if c.Skip != "" && strings.EqualFold(c.Name, n.file) {
						shown = true // the launcher already says why
					}
				}
				if !shown {
					m.Notes = append(m.Notes, Note{"warn", n.text})
				}
			}
		}
		m.Notes = append(m.Notes, nativeNotes(m)...)
		m.Notes = append(m.Notes, spaceNotes(m)...)
	}

	// Conflicts: two mods change the same game file and only one version shows.
	// Between the Mod Loader's mods the one lower in the list loads later and
	// wins. The game loads ~mods .pak files before the Mod Loader's mods, so
	// those lose to any of them; between two of those the list doesn't decide.
	type pair struct{ a, b *PakMod } // a is higher in the list
	const (
		laterWins  = iota // both through the Mod Loader: b loads later
		aIsNative         // a only as a ~mods .pak: b wins
		bIsNative         // b only as a ~mods .pak: a wins
		bothNative        // both only as ~mods .pak files
		outcomes
	)
	// shared counts each game file once per pair of mods, however many of
	// their files carry it
	shared := map[pair]*[outcomes]int{}
	for _, by := range loaded {
		var ms []*PakMod
		for m := range by {
			ms = append(ms, m)
		}
		sort.Slice(ms, func(i, j int) bool { return at[ms[i]] < at[ms[j]] })
		for i := 0; i < len(ms); i++ {
			for k := i + 1; k < len(ms); k++ {
				a, b := ms[i], ms[k]
				t := shared[pair{a, b}]
				if t == nil {
					t = &[outcomes]int{}
					shared[pair{a, b}] = t
				}
				switch aLoader, bLoader := by[a], by[b]; {
				case aLoader && bLoader:
					t[laterWins]++
				case bLoader:
					t[aIsNative]++
				case aLoader:
					t[bIsNative]++
				default:
					t[bothNative]++
				}
			}
		}
	}
	pairs := make([]pair, 0, len(shared))
	for p := range shared {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if at[pairs[i].a] != at[pairs[j].a] {
			return at[pairs[i].a] < at[pairs[j].a]
		}
		return at[pairs[i].b] < at[pairs[j].b]
	})
	for _, p := range pairs {
		for o, n := range shared[p] {
			if n == 0 {
				continue
			}
			what := "1 game file is"
			if n > 1 {
				what = strconv.Itoa(n) + " game files are"
			}
			switch o {
			case laterWins:
				p.a.Notes = append(p.a.Notes, Note{"warn", what + " also changed by " + p.b.Key + ". That mod loads later, so its version is the one you see."})
				p.b.Notes = append(p.b.Notes, Note{"info", what + " also changed by " + p.a.Key + ". This mod loads later, so its version is the one you see."})
			case aIsNative, bIsNative:
				loser, winner := p.a, p.b
				if o == bIsNative {
					loser, winner = p.b, p.a
				}
				loser.Notes = append(loser.Notes, Note{"warn", what + " also changed by " + winner.Key + ". That mod's version is the one you see, because the game loads .pak files in ~mods before the Mod Loader's mods."})
				winner.Notes = append(winner.Notes, Note{"info", what + " also changed by " + loser.Key + ". This mod's version is the one you see, because the game loads .pak files in ~mods before the Mod Loader's mods."})
			case bothNative:
				p.a.Notes = append(p.a.Notes, Note{"info", what + " also changed by " + p.b.Key + ". The game loads both from ~mods by itself, so their order here doesn't decide which version you see."})
				p.b.Notes = append(p.b.Notes, Note{"info", what + " also changed by " + p.a.Key + ". The game loads both from ~mods by itself, so their order here doesn't decide which version you see."})
			}
		}
	}
	for _, m := range mods {
		sort.SliceStable(m.Notes, func(i, j int) bool { return m.Notes[i].Kind == "warn" && m.Notes[j].Kind != "warn" })
		if run != nil {
			if r, ok := run.Mods[strings.ToLower(m.Key)]; ok {
				m.LastRun = r
			}
		}
	}
}

// nativeNotes: a mod with .pak files in ~mods. The game loads those by itself,
// so the switch and the load order don't change them; Remove does. The note
// shows also when the mod is switched off, where it matters most. While the
// ~mods folder is in storage the game loads none of them: the note says what
// happens when the player switches back to Modded.
func nativeNotes(m *PakMod) []Note {
	n := 0
	for _, c := range m.Containers {
		if c.native {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	these := "its .pak files"
	if n == 1 && len(m.Containers) == 1 {
		these = "it"
	}
	if m.inStorage {
		even := "whatever the switch and the list say"
		if !m.Enabled {
			even = "even though it is switched off"
		}
		return []Note{{"info", "In storage now, so the game doesn't load " + these + ". When you switch back to Modded, the game loads " + these + " from the ~mods folder by itself, " + even + ". To stop that, use Remove."}}
	}
	if !m.Enabled {
		head := "Still loads"
		if n < len(m.Containers) {
			head = "Partly still loads"
		}
		return []Note{{"warn", head + ": the game loads .pak files in the ~mods folder by itself, so switching this off doesn't stop " + these + ". To stop " + these + ", use Remove."}}
	}
	return []Note{{"info", "The game loads .pak files in the ~mods folder by itself, so the switch and the place in this list don't change " + these + ". To stop " + these + ", use Remove."}}
}

// spaceNotes: a name that starts or ends with a space. loadorder.txt can't
// hold such a name (the Mod Loader and the launcher both trim the spaces), so
// the mod can't be switched off or moved. Names under ~mods\ only lose a space
// at the end.
func spaceNotes(m *PakMod) []Note {
	starts := m.Where != "tilde" && strings.TrimLeft(m.Key, " \t") != m.Key
	ends := strings.TrimRight(m.Key, " \t") != m.Key
	if !starts && !ends {
		return nil
	}
	what, where := "folder's", "starts"
	if m.IsFile {
		what = "file's"
	}
	if !starts {
		where = "ends"
	}
	return []Note{{"warn", "Can't be switched off or moved: this " + what + " name " + where + " with a space. Rename it without the space (Show opens its folder)."}}
}

// decodeText reads UTF-8 (with or without BOM) or UTF-16 LE text.
func decodeText(b []byte) string {
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		return string(utf16.Decode(u))
	}
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	return string(b)
}

// writeLoadOrder saves the order and on/off state of every mod.
func writeLoadOrder(loaderDir string, entries []orderEntry) error {
	var b strings.Builder
	b.WriteString("# AC8 mod load order - written by ACE8 Mod Launcher. This file is optional.\r\n")
	b.WriteString("# +Name = on, -Name = off. Top loads first; lower entries load later and win conflicts.\r\n")
	b.WriteString("# Mods not listed here are loaded after these, A to Z.\r\n")
	for _, e := range entries {
		if e.Enabled {
			b.WriteString("+" + e.Key + "\r\n")
		} else {
			b.WriteString("-" + e.Key + "\r\n")
		}
	}
	if err := os.MkdirAll(loaderDir, 0o755); err != nil {
		return err
	}
	p := join(loaderDir, "loadorder.txt")
	if old, err := os.ReadFile(p); err == nil && string(old) == b.String() {
		// Nothing changes: leave the file and its date alone, so the window,
		// which watches that date, doesn't reload for nothing.
		return nil
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

type orderEntry struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

// keepSkinListLast moves Skin Studio's skin list to the end: it must load after
// every other mod so the game uses the list that has all new skins in it.
func keepSkinListLast(entries []orderEntry) []orderEntry {
	var rest, last []orderEntry
	for _, e := range entries {
		if isSkinListKey(e.Key) {
			last = append(last, e)
		} else {
			rest = append(rest, e)
		}
	}
	return append(rest, last...)
}

// isSkinListKey: the load-order name of Skin Studio's skin list, as a folder
// or as loose files, in the Mod Loader's folder or in ~mods:
// "zz_SkinStudio_SkinList", "~mods\zz_SkinStudio_SkinList",
// "zz_SkinStudio_SkinList.utoc", "zz_SkinStudio_SkinList_P.pak".
func isSkinListKey(key string) bool {
	k := strings.ReplaceAll(key, "/", `\`)
	if len(k) > len(`~mods\`) && strings.EqualFold(k[:len(`~mods\`)], `~mods\`) {
		k = k[len(`~mods\`):]
	}
	if ext := strings.ToLower(filepath.Ext(k)); ext == ".utoc" || ext == ".pak" {
		k = k[:len(k)-len(ext)]
	}
	if len(k) > 2 && strings.EqualFold(k[len(k)-2:], "_P") {
		k = k[:len(k)-2]
	}
	return strings.EqualFold(k, skinListName)
}
