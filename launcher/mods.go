package main

// Pak mods (.utoc/.ucas, and *_P.pak from Mod Loader 1.3.0 on), found and
// ordered by exactly the rules the installed Mod Loader (dlls\main.dll) uses,
// so what the launcher shows is what the game loads.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

const maxSearchDepth = 6

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
func isPakSidecar(path string) bool {
	if !strings.HasSuffix(strings.ToLower(path), "_p.utoc") {
		return false
	}
	return isFile(strings.TrimSuffix(path, filepath.Ext(path)) + ".pak")
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
}

func collectUtoc(dir string, depth int, paks bool, out *[]*Container) {
	ents := listDir(dir, paks)
	for pass := 0; pass < 2; pass++ {
		for _, e := range ents {
			if e.isDir != (pass == 1) {
				continue
			}
			p := join(dir, e.name)
			if !e.isDir {
				if !(paks && isPakSidecar(p)) {
					*out = append(*out, &Container{Path: p, Name: e.name, Mtime: e.mtime})
				}
			} else if depth < maxSearchDepth && !e.isLink && !isSkippedDir(e.name) {
				collectUtoc(p, depth+1, paks, out)
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
	Loads      int          `json:"loads"` // containers the loader will mount next start
}

func findMods(dir, prefix, where string, paks bool) []*PakMod {
	var mods []*PakMod
	for _, e := range listDir(dir, paks) {
		p := join(dir, e.name)
		if !e.isDir && paks && isPakSidecar(p) {
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
			collectUtoc(p, 1, paks, &m.Containers)
		}
		mods = append(mods, m)
	}
	return mods
}

const skinListName = "zz_SkinStudio_SkinList"

// scanPakMods finds every mod, in the loader's order, with loadorder.txt applied.
func scanPakMods(loaderDir, tildeDir string) (mods []*PakMod, staleEntries []string) {
	paks := loaderPaks(loaderDir)
	found := findMods(loaderDir, "", "loader", paks)
	found = append(found, findMods(tildeDir, `~mods\`, "tilde", paks)...)

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
		describeMod(m)
	}
	checkContainers(mods)
	return mods, staleEntries
}

// checkContainers marks what the loader will skip among the mods that are on:
// a second container with the same name (the newer copy wins; a .pak and its
// .utoc count as one name) and a .utoc without its .ucas. A .pak needs a .ucas
// only when it comes with a .utoc.
func checkContainers(mods []*PakMod) {
	var plan []*Container
	owner := map[*Container]*PakMod{}
	for _, m := range mods {
		for _, c := range m.Containers {
			c.Skip = ""
			if m.Enabled {
				plan = append(plan, c)
				owner[c] = m
			}
		}
	}
	for i, c := range plan {
		if c.Skip != "" {
			continue
		}
		for _, d := range plan[i+1:] {
			if d.Skip != "" || containerKey(c.Name) != containerKey(d.Name) {
				continue
			}
			older, newer := d, c
			if d.Mtime.After(c.Mtime) {
				older, newer = c, d
			}
			older.Skip = "Not loaded: " + owner[newer].Key + " has a newer copy of " + newer.Name + ", and only one can load."
			if older == c {
				break
			}
		}
	}
	for _, c := range plan {
		base := strings.TrimSuffix(c.Path, filepath.Ext(c.Path))
		if isPakName(c.Name) && !isFile(base+".utoc") {
			continue // a .pak on its own
		}
		if c.Skip == "" && !isFile(base+".ucas") {
			base := strings.TrimSuffix(c.Name, filepath.Ext(c.Name))
			c.Skip = "Not loaded: " + base + ".ucas is missing next to " + c.Name + "."
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
func describeMod(m *PakMod) {
	for _, c := range m.Containers {
		base := strings.TrimSuffix(c.Path, filepath.Ext(c.Path))
		for _, ext := range []string{".utoc", ".ucas", ".pak"} {
			if st, err := os.Stat(base + ext); err == nil {
				c.Size += st.Size()
			}
		}
		m.Size += c.Size
		if c.Mtime.After(m.Mtime) {
			m.Mtime = c.Mtime
		}
		if toc, err := readUtoc(base + ".utoc"); err == nil {
			c.ids = toc.packageIDs()
			c.Files = toc.assetNames()
		}
	}
	if strings.EqualFold(m.Name, skinListName) || strings.EqualFold(m.Key, skinListName) {
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
	type use struct {
		mod *PakMod
		c   *Container
	}
	byID := map[uint64][]use{}
	for _, m := range mods {
		m.Notes = []Note{}
		if !m.Enabled {
			continue
		}
		for _, c := range m.Containers {
			if c.Skip != "" {
				m.Notes = append(m.Notes, Note{"warn", c.Skip})
				continue
			}
			for id := range c.ids {
				byID[id] = append(byID[id], use{m, c})
			}
		}
	}
	// conflicts: the mod that loads later wins
	type pair struct{ a, b *PakMod }
	shared := map[pair]int{}
	for _, us := range byID {
		for i := 0; i < len(us); i++ {
			for k := i + 1; k < len(us); k++ {
				if us[i].mod != us[k].mod {
					shared[pair{us[i].mod, us[k].mod}]++
				}
			}
		}
	}
	for p, n := range shared {
		what := "1 game file is"
		if n > 1 {
			what = strconv.Itoa(n) + " game files are"
		}
		p.a.Notes = append(p.a.Notes, Note{"warn", what + " also changed by " + p.b.Key + ". That mod loads later, so its version is the one you see."})
		p.b.Notes = append(p.b.Notes, Note{"info", what + " also changed by " + p.a.Key + ". This mod loads later, so its version is the one you see."})
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
		if strings.EqualFold(e.Key, skinListName) || strings.EqualFold(e.Key, `~mods\`+skinListName) {
			last = append(last, e)
		} else {
			rest = append(rest, e)
		}
	}
	return append(rest, last...)
}
