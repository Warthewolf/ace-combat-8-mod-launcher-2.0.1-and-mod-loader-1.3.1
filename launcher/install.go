package main

// Setting up UE4SS and the Mod Loader, adding mods from a zip or a folder, and
// removing / restoring mods.

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

// ---- setup -----------------------------------------------------------------

// bundleDir finds a copy of UE4SS and the Mod Loader to install from: the Mod
// Loader download unzipped next to the launcher's folder (for example
// "War’s Mod Loader 1.3.1", holding the Game folder people paste into the game),
// or an older launcher's "install files" folder. The newest Mod Loader wins.
// The launcher doesn't need one once modding is set up.
func bundleDir() string {
	bundleMu.Lock()
	defer bundleMu.Unlock()
	if time.Since(bundleAt) < 10*time.Second && bundleFor == exeDir {
		return bundleCached
	}
	best, bestVer := "", ""
	consider := func(d string) {
		dll := join(d, "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "dlls", "main.dll")
		if !isFile(dll) || !isFile(join(d, "Game", "Binaries", "Win64", "dwmapi.dll")) {
			return
		}
		v := loaderVersion(dll)
		if best == "" || (v != "unknown" && (bestVer == "unknown" || versionLess(bestVer, v))) {
			best, bestVer = d, v
		}
	}
	if exeDir != "" {
		consider(join(exeDir, "install files"))
		consider(exeDir)
		if parent := filepath.Dir(exeDir); parent != exeDir {
			if ents, err := os.ReadDir(parent); err == nil && len(ents) < 2000 {
				for _, e := range ents {
					if e.IsDir() && !samePath(join(parent, e.Name()), exeDir) {
						consider(join(parent, e.Name()))
					}
				}
			}
		}
	}
	bundleCached, bundleAt, bundleFor = best, time.Now(), exeDir
	return best
}

var (
	bundleMu     sync.Mutex
	bundleCached string
	bundleAt     time.Time
	bundleFor    string
)

// errNoBundle: nothing to install from.
var errNoBundle = errors.New("UE4SS and the Mod Loader aren't in the game yet. Copy the Game folder from the Mod Loader download into the ACE COMBAT 8 folder (let it merge the folders), or unzip the Mod Loader download next to the launcher's folder and press Install again")

var (
	reLoaderVersion = regexp.MustCompile(`(\d+\.\d+\.\d+)-ac8-`)
	reVersionAlone  = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}$`)
)

// loaderVersion reads the version a Mod Loader main.dll reports to UE4SS.
func loaderVersion(dll string) string {
	b, err := os.ReadFile(dll)
	if err != nil || len(b) > 8<<20 {
		return ""
	}
	// UTF-16 strings of the DLL: "1.1.0-ac8-…" (what it reports to UE4SS) or, when the
	// compiler built that one in code, the version on its own ("1.1.0").
	var alone string
	for start := 0; start < 2; start++ {
		u := make([]uint16, 0, len(b)/2)
		for i := start; i+1 < len(b); i += 2 {
			u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
		}
		text := string(utf16.Decode(u))
		if m := reLoaderVersion.FindStringSubmatch(text); m != nil {
			return m[1]
		}
		if alone == "" {
			for _, part := range strings.Split(text, "\x00") {
				if reVersionAlone.MatchString(part) {
					alone = part
					break
				}
			}
		}
	}
	if alone != "" {
		return alone
	}
	if m := reLoaderVersion.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return "unknown"
}

// versionLess compares "1.0.0" style versions.
func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		var x, y int
		if i < len(pa) {
			fmt.Sscan(pa[i], &x)
		}
		if i < len(pb) {
			fmt.Sscan(pb[i], &y)
		}
		if x != y {
			return x < y
		}
	}
	return false
}

type SetupStatus struct {
	UE4SS       bool   `json:"ue4ss"`
	Loader      string `json:"loader"`
	LoaderOn    bool   `json:"loaderOn"`
	Bundled     string `json:"bundled"`
	BundleFound bool   `json:"bundleFound"`
	VCRuntime   bool   `json:"vcRuntime"`
	NeedsSetup  bool   `json:"needsSetup"`
	CanUpdate   bool   `json:"canUpdate"`
}

func (g Game) setupStatus() SetupStatus {
	s := SetupStatus{VCRuntime: vcRuntimeInstalled()}
	ue := g.ue4ssRoot()
	win64 := filepath.Dir(ue)
	s.UE4SS = isFile(join(win64, "dwmapi.dll")) && isFile(join(ue, "UE4SS.dll"))
	ld := join(ue, "Mods", "IoStoreLoaderMod")
	if isFile(join(ld, "dlls", "main.dll")) {
		s.Loader = loaderVersion(join(ld, "dlls", "main.dll"))
		txt := modsTxtState(join(ue, "Mods"))
		on, listed := txt["iostoreloadermod"]
		s.LoaderOn = (listed && on) || (isFile(join(ld, "enabled.txt")) && !(listed && !on))
	}
	if bd := bundleDir(); bd != "" {
		s.BundleFound = true
		s.Bundled = loaderVersion(join(bd, "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "dlls", "main.dll"))
	}
	s.NeedsSetup = !s.UE4SS || s.Loader == "" || !s.LoaderOn
	s.CanUpdate = s.BundleFound && s.Loader != "" && s.Bundled != "" && s.Bundled != "unknown" && (s.Loader == "unknown" || versionLess(s.Loader, s.Bundled))
	return s
}

// files setup never replaces: they hold the player's own settings and lists.
var keepFiles = map[string]bool{
	"game/binaries/win64/ue4ss/ue4ss-settings.ini":                         true,
	"game/binaries/win64/ue4ss/mods/mods.txt":                              true,
	"game/binaries/win64/ue4ss/mods/mods.json":                             true,
	"game/binaries/win64/ue4ss/mods/iostoreloadermod/loadorder.txt":        true,
	"game/binaries/win64/ue4ss/mods/iostoreloadermod/launcher.cfg":         true,
	"game/binaries/win64/ue4ss/mods/iostoreloadermod/ac8iostoreloader.log": true,
}

type SetupResult struct {
	Installed []string `json:"installed"`
	Kept      []string `json:"kept"`
}

// setup installs UE4SS and the Mod Loader from the "install files" folder next to
// the program, or brings an older Mod Loader up to date. It never replaces the
// player's UE4SS, settings or lists.
func (g Game) setup() (SetupResult, error) {
	var res SetupResult
	if gameRunning() {
		return res, errGameRunning
	}
	mi := g.modeInfo()
	if mi.Mode == modeUnmodded || mi.Mode == modeMixed {
		return res, errors.New("switch to Modded first, then set up")
	}
	st := g.setupStatus()
	if !st.BundleFound && st.UE4SS && st.Loader != "" {
		// already installed: only switch the Mod Loader on; no install files needed
		ld := join(g.Win64(), "ue4ss", "Mods", "IoStoreLoaderMod")
		if !isFile(join(ld, "enabled.txt")) {
			os.WriteFile(join(ld, "enabled.txt"), nil, 0o644)
		}
		setModsTxtLine(join(g.Win64(), "ue4ss", "Mods"), "IoStoreLoaderMod", true, false)
		logf("setup: Mod Loader switched on (no install files)")
		return res, nil
	}
	if !st.BundleFound {
		return res, errNoBundle
	}
	updateLoader := st.Loader == "" || st.CanUpdate
	if st.CanUpdate {
		// keep the old loader, in case anyone wants it back
		old := join(g.Win64(), "ue4ss", "Mods", "IoStoreLoaderMod", "dlls", "main.dll")
		dst := join(g.RemovedDir(), time.Now().Format("2006-01-02 150405")+" Mod Loader "+st.Loader)
		if err := copyFile(old, join(dst, "main.dll"), 0o644); err == nil {
			writeRemovedInfo(dst, removedInfo{Kind: "modloader", Name: "Mod Loader " + st.Loader, From: old, Removed: time.Now()})
		}
	}
	bd := bundleDir()
	err := filepath.Walk(bd, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(bd, p)
		key := strings.ToLower(filepath.ToSlash(rel))
		dst := join(g.Root, rel)
		inLoader := strings.HasPrefix(key, "game/binaries/win64/ue4ss/mods/iostoreloadermod/")
		if !strings.HasPrefix(key, "game/") {
			return nil // only the game's own folders are set up; nothing else is changed
		}
		if b := path.Base(key); b == "modlauncher.bat" || b == "modlauncher.ps1" {
			return nil // the Mod Loader's old launcher script: this launcher does its job
		}
		switch {
		case keepFiles[key] && isFile(dst):
			return nil
		case inLoader && !updateLoader && isFile(dst):
			return nil
		case !inLoader && isFile(dst):
			if !strings.HasSuffix(key, ".txt") {
				res.Kept = append(res.Kept, rel)
			}
			return nil // UE4SS, dwmapi.dll …: keep what the player has
		}
		if err := copyFile(p, dst, 0o644); err != nil {
			return fmt.Errorf("could not copy %s: %v", rel, err)
		}
		res.Installed = append(res.Installed, rel)
		return nil
	})
	if err != nil {
		return res, err
	}
	ld := join(g.Win64(), "ue4ss", "Mods", "IoStoreLoaderMod")
	if !isFile(join(ld, "enabled.txt")) {
		os.WriteFile(join(ld, "enabled.txt"), nil, 0o644)
	}
	setModsTxtLine(join(g.Win64(), "ue4ss", "Mods"), "IoStoreLoaderMod", true, false)
	logf("setup: installed %d files, kept %v", len(res.Installed), res.Kept)
	return res, nil
}

// ---- adding mods ---------------------------------------------------------------

type incoming struct {
	rel   string // with "/" separators, relative to the download
	open  func() (io.ReadCloser, error)
	size  int64
	mtime time.Time
}

type AddChoice struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Files []string `json:"files"`
}

type AddResult struct {
	Added      []string    `json:"added"`
	Updated    []string    `json:"updated"`
	Notes      []string    `json:"notes"`
	Choices    []AddChoice `json:"choices,omitempty"` // several versions of a mod: the player picks
	SkinStudio bool        `json:"skinStudio"`
}

func readZip(p string) ([]incoming, func(), error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, nil, errors.New("this .zip could not be opened (" + err.Error() + ")")
	}
	var out []incoming
	var total uint64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		total += f.UncompressedSize64
		if total > 32<<30 || len(out) > 50000 {
			zr.Close()
			return nil, nil, errors.New("this .zip is too big to be a mod")
		}
		f := f
		out = append(out, incoming{rel: f.Name, open: func() (io.ReadCloser, error) { return f.Open() }, size: int64(f.UncompressedSize64), mtime: f.Modified})
	}
	return out, func() { zr.Close() }, nil
}

func readFolder(root string) ([]incoming, error) {
	var out []incoming
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		p2 := p
		out = append(out, incoming{rel: filepath.ToSlash(rel), open: func() (io.ReadCloser, error) { return os.Open(p2) }, size: info.Size(), mtime: info.ModTime()})
		if len(out) > 50000 {
			return errors.New("this folder has too many files to be a mod")
		}
		return nil
	})
	return out, err
}

// a unit is one thing that gets installed as a whole: a mod folder, a script mod, a loose file triple.
type unit struct {
	id      string
	kind    string // "pak", "script", "tilde", "logic"
	name    string
	target  string // folder (or file, for loose ~mods files) it goes to
	files   map[string]incoming
	utocs   []string // its containers: .utoc files, and *_P.pak files (a .pak's own .utoc is not counted)
	isFiles bool     // target is a folder that receives loose files directly (not replaced as a whole)
}

var skipExt = map[string]bool{".zip": true, ".7z": true, ".rar": true, ".exe": true, ".bat": true, ".ps1": true, ".cmd": true, ".lnk": true}

func cleanSegments(rel string) ([]string, bool) {
	rel = strings.ReplaceAll(rel, `\`, "/")
	var segs []string
	for _, s := range strings.Split(rel, "/") {
		if s == "" || s == "." {
			continue
		}
		if s == ".." {
			return nil, false
		}
		s = strings.TrimRight(s, ". ") // Windows drops these itself; doing it here keeps names matching
		if s == "" {
			return nil, false
		}
		segs = append(segs, s)
	}
	if len(segs) == 0 {
		return nil, false
	}
	if strings.EqualFold(segs[0], "__MACOSX") {
		return nil, false
	}
	base := segs[len(segs)-1]
	if strings.HasPrefix(base, "._") || strings.EqualFold(base, ".DS_Store") || strings.EqualFold(base, "Thumbs.db") || strings.EqualFold(base, "desktop.ini") {
		return nil, false
	}
	for _, s := range segs {
		if !windowsSafeName(s) {
			return nil, false
		}
	}
	return segs, true
}

var reservedNames = map[string]bool{"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true}

// windowsSafeName rejects names Windows can't store or treats specially
// (drive letters, streams, device names, trailing dots or spaces).
func windowsSafeName(s string) bool {
	if s == "" || strings.ContainsAny(s, `:*?"<>|`) || strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") {
		return false
	}
	for _, r := range s {
		if r < 32 {
			return false
		}
	}
	stem := strings.ToLower(s)
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	return !reservedNames[strings.TrimSpace(stem)]
}

func indexFold(segs []string, name string) int {
	for i, s := range segs {
		if strings.EqualFold(s, name) {
			return i
		}
	}
	return -1
}

func safeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`<>:"/\|?*`, r) || r < 32 {
			return '_'
		}
		return r
	}, s)
	s = strings.TrimRight(s, ". ")
	if s == "" {
		s = "New mod"
	}
	return s
}

// addPlaces says where added mods go: into the game while Modded, and into the
// switched-off copy while Unmodded, so that Unmodded stays exactly as it is.
type addPlaces struct{ ue, loader, tilde, logic string }

func (g Game) addPlaces() (addPlaces, error) {
	base := g.Root
	switch g.modeInfo().Mode {
	case modeMixed:
		return addPlaces{}, errors.New("the last switch did not finish. Press Modded or Unmodded on the Play page first, then add the mod")
	case modeUnmodded:
		base = g.OffDir()
	}
	ue := join(base, "Game", "Binaries", "Win64", "ue4ss")
	paks := join(base, "Game", "Content", "Paks")
	return addPlaces{ue: ue, loader: join(ue, "Mods", "IoStoreLoaderMod"), tilde: join(paks, "~mods"), logic: join(paks, "LogicMods")}, nil
}

// planAdd works out where every file of a download goes.
func (g Game) planAdd(source string, files []incoming, at addPlaces) ([]*unit, AddResult, error) {
	var res AddResult
	ue, loader, tilde, logic := at.ue, at.loader, at.tilde, at.logic
	units := map[string]*unit{}
	var order []string
	get := func(id, kind, name, target string) *unit {
		if u, ok := units[id]; ok {
			return u
		}
		u := &unit{id: id, kind: kind, name: name, target: target, files: map[string]incoming{}}
		units[id] = u
		order = append(order, id)
		return u
	}
	note := func(s string) {
		for _, n := range res.Notes {
			if n == s {
				return
			}
		}
		res.Notes = append(res.Notes, s)
	}
	var loose []incoming
	for _, f := range files {
		segs, ok := cleanSegments(f.rel)
		if !ok {
			continue
		}
		f.rel = strings.Join(segs, "/")
		base := segs[len(segs)-1]
		ext := strings.ToLower(path.Ext(base))
		if strings.EqualFold(base, "skinstudio.json") {
			res.SkinStudio = true
			return nil, res, nil
		}
		if ext == ".zip" || ext == ".7z" || ext == ".rar" {
			note("The download has another archive inside (" + base + "). Unpack it and add what is inside, if it is a mod.")
			continue
		}
		if i := indexFold(segs, "ue4ss"); i >= 0 {
			rest := segs[i+1:]
			if len(rest) < 2 || !strings.EqualFold(rest[0], "Mods") {
				note("UE4SS itself was in the download; your own UE4SS was kept.")
				continue
			}
			rest = rest[1:] // after "Mods"
			if len(rest) == 1 {
				if strings.EqualFold(rest[0], "mods.txt") || strings.EqualFold(rest[0], "mods.json") {
					note("The download's mods.txt was not used; your own was kept and the new mods were switched on.")
				}
				continue
			}
			if strings.EqualFold(rest[0], "IoStoreLoaderMod") {
				sub := rest[1:] // inside the Mod Loader's folder
				switch {
				case len(sub) == 1:
					switch strings.ToLower(path.Ext(sub[0])) {
					case ".utoc", ".ucas", ".pak", ".sig": // a loose container: it gets a folder of its own
						name := safeName(strings.TrimSuffix(sub[0], path.Ext(sub[0])))
						u := get("pak:"+strings.ToLower(name), "pak", name, join(loader, name))
						u.files[sub[0]] = f
					}
				case strings.EqualFold(sub[0], "dlls") || strings.EqualFold(sub[0], "source"):
					note("The download has its own copy of the Mod Loader; yours was kept.")
				case isSkippedDir(sub[0]):
					// tools' working folders and old copies are not mods
				default:
					u := get("pak:"+strings.ToLower(sub[0]), "pak", sub[0], join(loader, sub[0]))
					u.files[strings.Join(sub[1:], "/")] = f
				}
				continue
			}
			name := rest[0]
			if strings.EqualFold(name, "shared") {
				note("The download's copy of UE4SS's shared files was not used; yours were kept.")
				continue
			}
			u := get("script:"+strings.ToLower(name), "script", name, join(ue, "Mods", name))
			u.files[strings.Join(rest[1:], "/")] = f
			continue
		}
		if i := indexFold(segs, "~mods"); i >= 0 && i+1 < len(segs) {
			rest := segs[i+1:]
			if len(rest) == 1 {
				name := strings.TrimSuffix(rest[0], path.Ext(rest[0]))
				u := get("tildefile:"+strings.ToLower(name), "tildefiles", name, tilde)
				u.isFiles = true
				u.files[rest[0]] = f
			} else {
				u := get("tilde:"+strings.ToLower(rest[0]), "tilde", rest[0], join(tilde, rest[0]))
				u.files[strings.Join(rest[1:], "/")] = f
			}
			continue
		}
		if i := indexFold(segs, "LogicMods"); i >= 0 && i+1 < len(segs) {
			rest := segs[i+1:]
			if len(rest) == 1 {
				u := get("logicfile:"+strings.ToLower(rest[0]), "logicfiles", rest[0], logic)
				u.isFiles = true
				u.files[rest[0]] = f
			} else {
				u := get("logic:"+strings.ToLower(rest[0]), "logic", rest[0], join(logic, rest[0]))
				u.files[strings.Join(rest[1:], "/")] = f
			}
			continue
		}
		if indexFold(segs, "Win64") >= 0 || indexFold(segs, "EasyAntiCheat") >= 0 {
			note("Game files in the download (such as " + base + ") were not used; the launcher only adds mods.")
			continue
		}
		loose = append(loose, f)
	}

	// Loose files: script mod folders first (a folder with Scripts\main.lua or dlls\main.dll) …
	scriptRoots := map[string]bool{}
	for _, f := range loose {
		segs := strings.Split(f.rel, "/")
		n := len(segs)
		if n >= 2 && ((strings.EqualFold(segs[n-2], "Scripts") && strings.EqualFold(segs[n-1], "main.lua")) || (strings.EqualFold(segs[n-2], "dlls") && strings.EqualFold(segs[n-1], "main.dll"))) {
			scriptRoots[strings.Join(segs[:n-2], "/")] = true
		}
	}
	var rest []incoming
	for _, f := range loose {
		placed := false
		for root := range scriptRoots {
			if root == "" || strings.HasPrefix(strings.ToLower(f.rel), strings.ToLower(root)+"/") {
				name := path.Base(root)
				if root == "" {
					name = source
				}
				name = safeName(name)
				if strings.EqualFold(name, "IoStoreLoaderMod") || strings.EqualFold(name, "shared") {
					// The Mod Loader (or UE4SS's shared files) is not a script mod: keep the
					// player's own copy, but still pick up pak mods in folders inside it.
					sub := strings.TrimPrefix(f.rel[len(root):], "/")
					first := strings.SplitN(sub, "/", 2)[0]
					if !strings.Contains(sub, "/") || isSkippedDir(first) {
						note("The download has its own copy of the Mod Loader or of UE4SS's files; yours were kept.")
						placed = true
					}
					break
				}
				placed = true
				u := get("script:"+strings.ToLower(name), "script", name, join(ue, "Mods", name))
				r := f.rel
				if root != "" {
					r = f.rel[len(root)+1:]
				}
				u.files[r] = f
				break
			}
		}
		if !placed {
			rest = append(rest, f)
		}
	}
	// … then pak mods: each folder that holds .utoc or *_P.pak files is one mod.
	pakDirs := map[string][]incoming{}
	for _, f := range rest {
		if strings.HasSuffix(strings.ToLower(f.rel), ".utoc") || isPakName(f.rel) {
			pakDirs[path.Dir(f.rel)] = nil
		}
	}
	for _, f := range rest {
		d := path.Dir(f.rel)
		if _, ok := pakDirs[d]; ok && !skipExt[strings.ToLower(path.Ext(f.rel))] {
			pakDirs[d] = append(pakDirs[d], f)
		}
	}
	dirs := make([]string, 0, len(pakDirs))
	for d := range pakDirs {
		dirs = append(dirs, d)
	}
	sort.Slice(dirs, func(i, j int) bool { return foldCmp(dirs[i], dirs[j]) < 0 })
	for _, d := range dirs {
		name := path.Base(d)
		if d == "." {
			name = source
		}
		name = safeName(name)
		id := "pak:" + strings.ToLower(name)
		if _, taken := units[id]; taken {
			id = "pak:" + strings.ToLower(d)
			name = safeName(strings.ReplaceAll(d, "/", " - "))
		}
		u := get(id, "pak", name, join(loader, name))
		for _, f := range pakDirs[d] {
			u.files[path.Base(f.rel)] = f
		}
	}
	var out []*unit
	for _, id := range order {
		u := units[id]
		for name := range u.files {
			low := strings.ToLower(name)
			switch {
			case isPakName(name):
				u.utocs = append(u.utocs, path.Base(name))
			case strings.HasSuffix(low, ".utoc"):
				// a *_P.utoc next to a .pak of the same name belongs to the .pak
				if strings.HasSuffix(low, "_p.utoc") && hasFileFold(u.files, strings.TrimSuffix(path.Base(name), path.Ext(name))+".pak") {
					continue
				}
				u.utocs = append(u.utocs, path.Base(name))
			}
		}
		sort.Strings(u.utocs)
		out = append(out, u)
	}
	return out, res, nil
}

// addMods installs a download. Several versions of the same mod side by side
// (folders that each hold a mod) are offered as a choice first; pick names the
// ones to install.
func (g Game) addMods(source string, files []incoming, pick []string) (AddResult, error) {
	if gameRunning() {
		return AddResult{}, errGameRunning
	}
	st := g.setupStatus()
	if st.Loader == "" {
		return AddResult{}, errors.New("set up modding first (Play page), then add mods")
	}
	at, err := g.addPlaces()
	if err != nil {
		return AddResult{}, err
	}
	units, res, err := g.planAdd(source, files, at)
	if err != nil || res.SkinStudio {
		return res, err
	}
	var paks []*unit
	for _, u := range units {
		if u.kind == "pak" && len(u.utocs) > 0 {
			paks = append(paks, u)
		}
	}
	if len(units) == 0 {
		for _, f := range files {
			if strings.EqualFold(path.Ext(f.rel), ".pak") && !isPakName(f.rel) {
				return res, errors.New(path.Base(f.rel) + " can't be loaded: the Mod Loader only loads .pak files whose names end in _P.pak. Ask the mod's author for a _P.pak version")
			}
		}
		return res, errors.New("no mod was found in it. A mod has a .utoc and a .ucas file, or a file ending in _P.pak (pak mods), or a Scripts\\main.lua (UE4SS script mod)")
	}
	if len(paks) > 1 && pick == nil && looksLikeVersions(paks) {
		for _, u := range paks {
			var fl []string
			for n := range u.files {
				fl = append(fl, n)
			}
			sort.Strings(fl)
			res.Choices = append(res.Choices, AddChoice{ID: u.id, Name: u.name, Files: fl})
		}
		return res, nil
	}
	if pick != nil {
		want := map[string]bool{}
		for _, p := range pick {
			want[p] = true
		}
		var keep []*unit
		for _, u := range units {
			if u.kind != "pak" || want[u.id] {
				keep = append(keep, u)
			}
		}
		units = keep
	}
	installed, _ := scanPakMods(at.loader, at.tilde)
	if !loaderPaks(at.loader) {
		for _, u := range units {
			for _, n := range u.utocs {
				if isPakName(n) {
					res.Notes = append(res.Notes, u.name+" is a .pak mod, which needs Mod Loader 1.3.1 or newer. Update it in Settings > Modding setup.")
					break
				}
			}
		}
	}
	stage := join(g.Storage(), "Adding")
	os.RemoveAll(stage) // left over from a launcher that was closed while adding
	defer os.RemoveAll(stage)
	needsBPModLoader := false
	for i, u := range units {
		if len(u.files) == 0 {
			continue
		}
		if u.kind != "script" {
			for _, n := range u.utocs {
				stem := strings.TrimSuffix(n, path.Ext(n))
				if isPakName(n) && !hasFileFold(u.files, stem+".utoc") {
					continue // a .pak on its own needs nothing else
				}
				if !isFile(join(u.target, stem+".ucas")) && !hasFileFold(u.files, stem+".ucas") {
					res.Notes = append(res.Notes, n+" has no .ucas file next to it in the download, so it will not load. Ask the mod's author for the full files.")
				}
			}
		}
		// 1. Copy the new files next to the game first. Nothing in the game is
		// touched until every file of the download has been read.
		tmp := join(stage, strconv.Itoa(i))
		if err := stageUnit(u, tmp); err != nil {
			return res, fmt.Errorf("%s could not be added: %v", u.name, err)
		}
		// 2. Move older copies out of the way, into Removed mods.
		var putBack []string
		undo := func() {
			for k := len(putBack) - 1; k >= 0; k-- {
				if err := g.restoreRemoved(filepath.Base(putBack[k])); err != nil {
					logf("could not put %s back: %v", putBack[k], err)
				}
			}
		}
		older := map[string]bool{}
		if u.kind != "script" {
			// the same container already installed in another folder is an older copy of this mod
			for _, m := range installed {
				if samePath(m.Path, u.target) || older[strings.ToLower(m.Path)] || !exists(m.Path) {
					continue
				}
				if u.isFiles && m.IsFile && samePath(filepath.Dir(m.Path), u.target) {
					continue // a loose file with the same name: replaced below, as an update
				}
				for _, c := range m.Containers {
					for _, n := range u.utocs {
						if containerKey(c.Name) == containerKey(n) && !older[strings.ToLower(m.Path)] {
							older[strings.ToLower(m.Path)] = true
							kind := "pak"
							if m.Where == "tilde" {
								kind = "tilde"
							}
							if d, err := g.removeToStorage(kind, m.Key, m.Path, m.IsFile); err == nil {
								putBack = append(putBack, d)
								res.Notes = append(res.Notes, "The older copy in "+m.Key+" was moved to Removed mods.")
							}
						}
					}
				}
			}
		}
		updated, wasOff := false, false
		if u.isFiles {
			// loose files in ~mods or LogicMods: a file with the same name is an older copy
			for rel := range u.files {
				dst := join(u.target, filepath.FromSlash(rel))
				if !exists(dst) {
					continue
				}
				kind, name := "tilde", "~mods\\"+strings.TrimSuffix(path.Base(rel), path.Ext(rel))
				if u.kind == "logicfiles" {
					kind, name = "logic", strings.TrimSuffix(path.Base(rel), path.Ext(rel))
				}
				d, err := g.removeToStorage(kind, name, dst, true)
				if err != nil {
					undo()
					return res, fmt.Errorf("could not replace %s: %s", path.Base(rel), explainMoveError(err))
				}
				putBack = append(putBack, d)
				updated = true
			}
		} else if exists(u.target) {
			wasOff = u.kind == "script" && isFile(join(u.target, "enabled.off")) && !isFile(join(u.target, "enabled.txt"))
			d, err := g.removeToStorage(u.kind, u.name, u.target, false)
			if err != nil {
				undo()
				return res, fmt.Errorf("could not replace %s: %s", u.name, explainMoveError(err))
			}
			putBack = append(putBack, d)
			updated = true
		}
		// 3. Move the new copy in. If that fails, the older copies go back.
		if err := commitUnit(u, tmp); err != nil {
			undo()
			return res, fmt.Errorf("%s could not be added: %s Nothing was changed for this mod.", u.name, explainMoveError(err))
		}
		if u.kind == "script" {
			if !isFile(join(u.target, "enabled.txt")) && !isFile(join(u.target, "enabled.off")) {
				os.WriteFile(join(u.target, "enabled.txt"), nil, 0o644)
			}
			if wasOff {
				setScriptMod(at.ue, u.name, false) // an update keeps a switched-off mod switched off
			} else {
				setModsTxtLine(filepath.Dir(u.target), u.name, true, false)
			}
		}
		if strings.HasPrefix(u.kind, "logic") {
			needsBPModLoader = true
		}
		label := u.name
		if u.kind == "script" {
			label += " (script mod)"
		}
		if updated {
			res.Updated = append(res.Updated, label)
		} else {
			res.Added = append(res.Added, label)
		}
	}
	if needsBPModLoader && !isDir(join(at.ue, "Mods", "BPModLoaderMod")) {
		res.Notes = append(res.Notes, "LogicMods need UE4SS's BPModLoaderMod, which is not installed, so this mod won't load until it is. It comes with the full UE4SS download (ue4ss\\Mods\\BPModLoaderMod).")
	}
	g.tidyLoadOrder()
	logf("added from %s: added %v, updated %v, notes %v", source, res.Added, res.Updated, res.Notes)
	return res, nil
}

func hasFileFold(m map[string]incoming, name string) bool {
	for k := range m {
		if strings.EqualFold(path.Base(k), name) {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// looksLikeVersions: several pak mods in one download are usually alternatives
// ("Blue", "Red") when they replace the same game files, or have the same file names.
func looksLikeVersions(paks []*unit) bool {
	names := map[string]int{}
	for _, u := range paks {
		for _, n := range u.utocs {
			names[containerKey(n)]++
		}
	}
	for _, c := range names {
		if c > 1 {
			return true
		}
	}
	ids := map[uint64]int{}
	for _, u := range paks {
		seen := map[uint64]bool{}
		for name, f := range u.files {
			if !strings.HasSuffix(strings.ToLower(name), ".utoc") || f.size > 64<<20 {
				continue
			}
			rc, err := f.open()
			if err != nil {
				continue
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			if toc, err := parseUtoc(b); err == nil {
				for id := range toc.packageIDs() {
					if !seen[id] {
						seen[id] = true
						ids[id]++
					}
				}
			}
		}
	}
	for _, c := range ids {
		if c > 1 {
			return true
		}
	}
	return false
}

// stageUnit writes a unit's files into tmp (in the storage folder, on the same
// drive as the game).
func stageUnit(u *unit, tmp string) error {
	os.RemoveAll(tmp)
	for rel, f := range u.files {
		if err := writeIncoming(f, join(tmp, filepath.FromSlash(rel))); err != nil {
			os.RemoveAll(tmp)
			return err
		}
	}
	return nil
}

// commitUnit moves a staged unit into place. On failure nothing of it is left behind.
func commitUnit(u *unit, tmp string) error {
	if !u.isFiles {
		if lexists(u.target) {
			return errors.New(u.target + " is in the way")
		}
		if err := os.MkdirAll(filepath.Dir(u.target), 0o755); err != nil {
			return err
		}
		// one move when Windows allows it; file by file when something (often a
		// virus scanner looking at the new files) holds the folder for a moment
		if err := moveInto(tmp, u.target, moveItem); err != nil {
			os.RemoveAll(u.target)
			return err
		}
		return nil
	}
	if err := os.MkdirAll(u.target, 0o755); err != nil {
		return err
	}
	var moved []string
	for rel := range u.files {
		dst := join(u.target, filepath.FromSlash(rel))
		if err := moveItem(join(tmp, filepath.FromSlash(rel)), dst); err != nil {
			for _, m := range moved {
				os.Remove(m)
			}
			return err
		}
		moved = append(moved, dst)
	}
	return nil
}

func writeIncoming(f incoming, dst string) error {
	rc, err := f.open()
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		os.Remove(dst)
		return fmt.Errorf("could not write %s: %v", filepath.Base(dst), err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	if !f.mtime.IsZero() {
		os.Chtimes(dst, f.mtime, f.mtime)
	}
	return nil
}

// tidyLoadOrder rewrites loadorder.txt with every mod in its current place,
// Skin Studio's skin list last, and names that no longer exist left out.
func (g Game) tidyLoadOrder() {
	ld := g.loaderDir()
	if !isDir(ld) {
		return
	}
	mods, _ := scanPakMods(ld, g.tildeModsDir())
	var e []orderEntry
	for _, m := range mods {
		e = append(e, orderEntry{Key: m.Key, Enabled: m.Enabled})
	}
	writeLoadOrder(ld, keepSkinListLast(e))
}

// ---- removing and restoring ------------------------------------------------------

type removedInfo struct {
	Kind    string    `json:"kind"`
	Name    string    `json:"name"`
	From    string    `json:"from"`
	Files   []string  `json:"files,omitempty"` // for a mod that was loose files
	Removed time.Time `json:"removed"`
}

type RemovedMod struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Name    string    `json:"name"`
	From    string    `json:"from"`
	Removed time.Time `json:"removed"`
	Size    int64     `json:"size"`
}

func writeRemovedInfo(dir string, ri removedInfo) {
	b, _ := json.MarshalIndent(ri, "", "  ")
	os.WriteFile(join(dir, "removed.json"), b, 0o644)
}

// removeToStorage moves a mod into "Removed mods", from where it can be restored.
func (g Game) removeToStorage(kind, name, p string, looseFile bool) (string, error) {
	dir := join(g.RemovedDir(), time.Now().Format("2006-01-02 150405")+" "+safeName(strings.ReplaceAll(name, `\`, " ")))
	for n := 2; exists(dir); n++ {
		dir = join(g.RemovedDir(), fmt.Sprintf("%s %s %d", time.Now().Format("2006-01-02 150405"), safeName(strings.ReplaceAll(name, `\`, " ")), n))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ri := removedInfo{Kind: kind, Name: name, From: p, Removed: time.Now()}
	giveUp := func(err error) (string, error) {
		os.Remove(join(dir, "removed.json"))
		os.Remove(dir)
		return "", err
	}
	if looseFile {
		// a loose mod is up to four files with the same name; they move together or not at all
		base := strings.TrimSuffix(p, filepath.Ext(p))
		for _, ext := range []string{".utoc", ".ucas", ".pak", ".sig"} {
			if isFile(base + ext) {
				ri.Files = append(ri.Files, base+ext)
			}
		}
		writeRemovedInfo(dir, ri)
		for i, f := range ri.Files {
			if err := moveItem(f, join(dir, filepath.Base(f))); err != nil {
				for _, done := range ri.Files[:i] {
					moveItem(join(dir, filepath.Base(done)), done)
				}
				return giveUp(err)
			}
		}
	} else {
		writeRemovedInfo(dir, ri)
		if err := moveItem(p, join(dir, filepath.Base(p))); err != nil {
			return giveUp(err)
		}
	}
	logf("removed %s %s (%s) to %s", kind, name, p, dir)
	return dir, nil
}

func (g Game) listRemoved() []RemovedMod {
	ents, _ := os.ReadDir(g.RemovedDir())
	var out []RemovedMod
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := join(g.RemovedDir(), e.Name())
		var ri removedInfo
		b, err := os.ReadFile(join(dir, "removed.json"))
		if err != nil || json.Unmarshal(b, &ri) != nil {
			continue
		}
		var size int64
		filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				size += info.Size()
			}
			return nil
		})
		out = append(out, RemovedMod{ID: e.Name(), Kind: ri.Kind, Name: ri.Name, From: ri.From, Removed: ri.Removed, Size: size})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Removed.After(out[j].Removed) })
	return out
}

func (g Game) removedEntry(id string) (string, removedInfo, error) {
	var ri removedInfo
	if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return "", ri, errors.New("unknown removed mod")
	}
	dir := join(g.RemovedDir(), id)
	b, err := os.ReadFile(join(dir, "removed.json"))
	if err != nil || json.Unmarshal(b, &ri) != nil {
		return "", ri, errors.New("that removed mod is no longer there")
	}
	return dir, ri, nil
}

// restoreRemoved puts a removed mod back where it was. If the game was switched
// in the meantime, it goes to where that folder is now.
func (g Game) restoreRemoved(id string) error {
	if gameRunning() {
		return errGameRunning
	}
	dir, ri, err := g.removedEntry(id)
	if err != nil {
		return err
	}
	if ri.Kind == "modloader" {
		return errors.New("an old Mod Loader is kept only as a backup; copy its main.dll by hand if you really need it")
	}
	from := g.relocate(ri.From)
	if len(ri.Files) > 0 {
		for _, f := range ri.Files {
			if exists(g.relocate(f)) {
				return errors.New(filepath.Base(f) + " is installed again already. Remove it first if you want this copy back")
			}
		}
		for _, f := range ri.Files {
			if err := moveItem(join(dir, filepath.Base(f)), g.relocate(f)); err != nil {
				return err
			}
		}
	} else {
		if exists(from) {
			return errors.New(filepath.Base(from) + " is installed again already. Remove that one first if you want this copy back")
		}
		if err := moveItem(join(dir, filepath.Base(from)), from); err != nil {
			return err
		}
	}
	os.Remove(join(dir, "removed.json"))
	os.Remove(dir)
	if ri.Kind == "pak" || ri.Kind == "tilde" {
		g.tidyLoadOrder()
	}
	logf("restored %s to %s", ri.Name, from)
	return nil
}

// relocate maps a path inside the game to where it is now (the ue4ss folder or
// ~mods may have been switched off since, or switched back on).
func (g Game) relocate(p string) string {
	for _, it := range parkItems {
		gp, pp := g.itemPaths(it)
		for _, pair := range [][2]string{{gp, pp}, {pp, gp}} {
			if strings.HasPrefix(strings.ToLower(p), strings.ToLower(pair[0])+string(filepath.Separator)) {
				alt := pair[1] + p[len(pair[0]):]
				if !hasFiles(pair[0]) && hasFiles(pair[1]) {
					return alt
				}
				return p
			}
		}
	}
	return p
}

func (g Game) deleteRemoved(id string) error {
	dir, _, err := g.removedEntry(id)
	if err != nil {
		return err
	}
	logf("deleted removed mod %s", dir)
	return os.RemoveAll(dir)
}
