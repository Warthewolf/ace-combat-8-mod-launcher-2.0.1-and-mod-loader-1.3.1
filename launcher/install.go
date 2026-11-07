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
	"unicode/utf8"
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
		if looksLikeGame(d) {
			// a game install (the launcher sits in it, or next to it in
			// steamapps\common) is not a download to set up from: it can't
			// bring back a file that is missing from the game
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
var errNoBundle = errors.New("UE4SS and the Mod Loader aren't in the game yet. Copy the Game folder from the Mod Loader download into the ACE COMBAT 8 folder (let it merge the folders), or unzip the Mod Loader download next to the launcher's folder, then press Set up modding on the Play page")

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
	bd := bundleDir()
	if !isFile(join(g.ue4ssRoot(), "UE4SS.dll")) && !isFile(join(bd, "Game", "Binaries", "Win64", "ue4ss", "UE4SS.dll")) {
		return res, errors.New("UE4SS.dll is missing from the Mod Loader download (in " + filepath.Base(bd) + "\\Game\\Binaries\\Win64\\ue4ss), so nothing was set up. Antivirus programs sometimes remove it: restore it from your antivirus's quarantine, or unzip the Mod Loader download again, then press Set up modding again")
	}
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
	if missing := g.setupMissing(); len(missing) > 0 {
		logf("setup: still missing %v", missing)
		return res, errors.New("modding is not set up yet: " + strings.Join(missing, ", ") + " is missing in the game. Antivirus programs sometimes remove these files: restore them from your antivirus's quarantine, or unzip the Mod Loader download again, then press Set up modding again")
	}
	return res, nil
}

// setupMissing names the files UE4SS and the Mod Loader need that are not in the game.
func (g Game) setupMissing() []string {
	var out []string
	ue := g.ue4ssRoot()
	for _, f := range []struct{ name, path string }{
		{"dwmapi.dll", join(filepath.Dir(ue), "dwmapi.dll")},
		{"UE4SS.dll", join(ue, "UE4SS.dll")},
		{"the Mod Loader's main.dll", join(ue, "Mods", "IoStoreLoaderMod", "dlls", "main.dll")},
	} {
		if !isFile(f.path) {
			out = append(out, f.name)
		}
	}
	return out
}

// ---- adding mods ---------------------------------------------------------------

type incoming struct {
	rel   string // with "/" separators, relative to the download
	open  func() (io.ReadCloser, error)
	size  int64
	mtime time.Time
}

// AddChoice is one version the player can pick. Its ID starts with its
// download's name ("<source>|…"), so the picks for two downloads added together
// can't be mixed up.
type AddChoice struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Files  []string `json:"files"`
	Source string   `json:"source"`
}

type AddResult struct {
	Added   []string `json:"added"`
	Updated []string `json:"updated"`
	Notes   []string `json:"notes"`
	// Warnings: problems that stop an added mod from loading, each saying what
	// to do about it
	Warnings   []string    `json:"warnings,omitempty"`
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
		out = append(out, incoming{rel: zipName(f.Name), open: func() (io.ReadCloser, error) { return f.Open() }, size: int64(f.UncompressedSize64), mtime: f.Modified})
	}
	return out, func() { zr.Close() }, nil
}

// zipName keeps apart names that are not UTF-8, as older Japanese, Chinese or
// Korean zip tools write them. Windows would turn every byte it can't read into
// the same sign, so the folders "青" and "赤" would both become "��" and one mod
// would replace the other. Each such byte is written as "_" and its value
// instead ("_90_C2"), which stays the same each time the zip is added. The byte
// after a lead byte belongs to the same letter, even when it looks like "\".
func zipName(name string) string {
	if utf8.ValidString(name) {
		return name
	}
	var b strings.Builder
	for i := 0; i < len(name); {
		r, n := utf8.DecodeRuneInString(name[i:])
		if r != utf8.RuneError || n != 1 {
			b.WriteString(name[i : i+n])
			i += n
			continue
		}
		c := name[i]
		fmt.Fprintf(&b, "_%02X", c)
		i++
		if c >= 0x81 && c <= 0xFE && i < len(name) && name[i] >= 0x40 && name[i] <= 0xFE && name[i] != 0x7F {
			fmt.Fprintf(&b, "_%02X", name[i])
			i++
		}
	}
	return b.String()
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
	base    string   // for a place named in the download (ue4ss\Mods, ~mods, LogicMods): the id without the folders above it
	pre     string   // … and those folders ("Option A - Green")
	alt     bool     // one of several versions of the same files in the download: the player picks one
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
	// Where the download names the place a file goes (ue4ss\Mods, ~mods,
	// LogicMods), the folders above that place keep copies apart:
	// "Option A\~mods\HUD_P.pak" and "Option B\~mods\HUD_P.pak" are two versions
	// of one mod, not one mod whose files overwrite each other.
	pre := ""
	getAt := func(id, kind, name, target string) *unit {
		u := get(id+"\x00"+strings.ToLower(pre), kind, name, target)
		u.base, u.pre = id, pre
		return u
	}
	// Skin Studio skins are added in Skin Studio; the rest of the download is added here.
	skins := skinStudioDirs(files)
	var loose []incoming
	for _, f := range files {
		segs, ok := cleanSegments(f.rel)
		if !ok {
			continue
		}
		f.rel = strings.Join(segs, "/")
		if inDirs(f.rel, skins) {
			continue
		}
		base := segs[len(segs)-1]
		ext := strings.ToLower(path.Ext(base))
		if ext == ".zip" || ext == ".7z" || ext == ".rar" {
			note("The download has another archive inside (" + base + "). Unpack it and add what is inside, if it is a mod.")
			continue
		}
		if i := indexFold(segs, "ue4ss"); i >= 0 {
			pre = strings.Join(segs[:i], "/")
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
						u := getAt("pak:"+strings.ToLower(name), "pak", name, join(loader, name))
						u.files[sub[0]] = f
					}
				case strings.EqualFold(sub[0], "dlls") || strings.EqualFold(sub[0], "source"):
					note("The download has its own copy of the Mod Loader; yours was kept.")
				case isSkippedDir(sub[0]):
					// tools' working folders and old copies are not mods
				default:
					u := getAt("pak:"+strings.ToLower(sub[0]), "pak", sub[0], join(loader, sub[0]))
					u.files[strings.Join(sub[1:], "/")] = f
				}
				continue
			}
			name := rest[0]
			if strings.EqualFold(name, "shared") {
				// UE4SS's shared Lua libraries: added file by file, never replaced as a whole
				if len(rest) > 1 {
					u := get("shared", "shared", "shared", join(ue, "Mods", "shared"))
					u.isFiles = true
					u.files[strings.Join(rest[1:], "/")] = f
				}
				continue
			}
			if keepStockScriptMod(ue, name) {
				note("UE4SS's own " + name + " was in the download; yours was kept.")
				continue
			}
			u := getAt("script:"+strings.ToLower(name), "script", name, join(ue, "Mods", name))
			u.files[strings.Join(rest[1:], "/")] = f
			continue
		}
		if i := indexFold(segs, "~mods"); i >= 0 && i+1 < len(segs) {
			pre = strings.Join(segs[:i], "/")
			rest := segs[i+1:]
			if len(rest) == 1 {
				name := strings.TrimSuffix(rest[0], path.Ext(rest[0]))
				u := getAt("tildefile:"+strings.ToLower(name), "tildefiles", name, tilde)
				u.isFiles = true
				u.files[rest[0]] = f
			} else {
				name := rest[0]
				if isSkippedDir(name) {
					// the Mod Loader skips a ~mods folder with this name, so the mod would never load
					name = modName(segs[:i], source)
				}
				u := getAt("tilde:"+strings.ToLower(name), "tilde", name, join(tilde, name))
				u.files[strings.Join(rest[1:], "/")] = f
			}
			continue
		}
		if i := indexFold(segs, "LogicMods"); i >= 0 && i+1 < len(segs) {
			pre = strings.Join(segs[:i], "/")
			rest := segs[i+1:]
			if len(rest) == 1 {
				// Thing.pak, Thing.ucas and Thing.utoc are one mod: they go in together
				name := strings.TrimSuffix(rest[0], path.Ext(rest[0]))
				u := getAt("logicfile:"+strings.ToLower(name), "logicfiles", name, logic)
				u.isFiles = true
				u.files[rest[0]] = f
			} else {
				u := getAt("logic:"+strings.ToLower(rest[0]), "logic", rest[0], join(logic, rest[0]))
				u.files[strings.Join(rest[1:], "/")] = f
			}
			continue
		}
		if i := indexFold(segs, "shared"); i > 0 && i+1 < len(segs) && strings.EqualFold(segs[i-1], "Mods") {
			// UE4SS's shared Lua libraries in a download laid out from inside the
			// ue4ss folder ("Mods\shared\…"): added file by file, as above
			u := get("shared", "shared", "shared", join(ue, "Mods", "shared"))
			u.isFiles = true
			u.files[strings.Join(segs[i+1:], "/")] = f
			continue
		}
		if indexFold(segs, "Win64") >= 0 || indexFold(segs, "EasyAntiCheat") >= 0 {
			note("Game files in the download (such as " + base + ") were not used; the launcher only adds mods.")
			continue
		}
		loose = append(loose, f)
	}
	units, order = joinAlternatives(units, order)

	// Loose files: script mod folders first (a folder with Scripts\main.lua or dlls\main.dll) …
	scriptRoots := map[string]bool{}
	pakFolders := map[string]bool{} // folders (lower case) with .utoc or *_P.pak files in them
	for _, f := range loose {
		segs := strings.Split(f.rel, "/")
		n := len(segs)
		if n >= 2 && ((strings.EqualFold(segs[n-2], "Scripts") && strings.EqualFold(segs[n-1], "main.lua")) || (strings.EqualFold(segs[n-2], "dlls") && strings.EqualFold(segs[n-1], "main.dll"))) {
			scriptRoots[strings.Join(segs[:n-2], "/")] = true
		}
		if strings.HasSuffix(strings.ToLower(f.rel), ".utoc") || isPakName(f.rel) {
			pakFolders[strings.ToLower(path.Dir(f.rel))] = true
		}
	}
	roots := make([]string, 0, len(scriptRoots))
	for r := range scriptRoots {
		roots = append(roots, r)
	}
	// the innermost script mod a file is in gets it
	sort.Slice(roots, func(i, j int) bool {
		return len(roots[i]) > len(roots[j]) || (len(roots[i]) == len(roots[j]) && roots[i] < roots[j])
	})
	rootNames := scriptNames(roots, source, ue)
	var rest []incoming
	for _, f := range loose {
		placed := false
		for _, root := range roots {
			if root == "" || strings.HasPrefix(strings.ToLower(f.rel), strings.ToLower(root)+"/") {
				name := rootNames[root]
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
				if isContainerFile(f.rel) && pakFolders[strings.ToLower(path.Dir(f.rel))] {
					// a pak mod that comes with the script mod: the Mod Loader only
					// loads it from its own folder, so it is added as a pak mod below
					break
				}
				placed = true
				if keepStockScriptMod(ue, name) {
					note("UE4SS's own " + name + " was in the download; yours was kept.")
					break
				}
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
	taken := func(id string) bool {
		for _, u := range units {
			if u.id == id || u.base == id {
				return true
			}
		}
		return false
	}
	for _, d := range dirs {
		var segs []string
		if d != "." {
			segs = strings.Split(d, "/")
		}
		// named after its folder, unless that says nothing ("Paks") or is one the
		// Mod Loader skips ("Packed"): then after the folder above, or the download
		name := modName(segs, source)
		id := "pak:" + strings.ToLower(name)
		if taken(id) {
			id = "pak:" + strings.ToLower(d)
			name = safeName(strings.ReplaceAll(d, "/", " - "))
			if isSkippedDir(name) {
				name += " mod"
			}
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
	if len(skins) > 0 {
		if len(out) == 0 {
			res.SkinStudio = true
			return nil, res, nil
		}
		var names []string
		for _, d := range skins {
			names = append(names, path.Base(d))
		}
		sort.Strings(names)
		what := "this skin was"
		if len(names) > 1 {
			what = "these skins were"
		}
		note("Skin Studio skins are added in ACE8 Skin Studio (Skin slots, then Import), so " + what + " not added here: " + strings.Join(names, ", ") + ".")
	}
	return out, res, nil
}

// genericDirs are folder names that say nothing about which mod is in them.
var genericDirs = map[string]bool{"paks": true, "content": true, "game": true, "mods": true, "mod": true,
	"files": true, "assets": true, "data": true, "windows": true, "windowsnoeditor": true,
	"iostoreloadermod": true, "new folder": true, "~mods": true, "logicmods": true}

// modName names a mod after the last folder in segs that says what it is: not a
// generic one ("Paks", "Files"), so that two different mods don't both become
// "Paks", and not one the Mod Loader skips ("Packed", "Old"), where the mod would
// never load or show on the Mods page. Without one, the download's name is used.
func modName(segs []string, source string) string {
	for i := len(segs) - 1; i >= 0; i-- {
		if !genericDirs[strings.ToLower(segs[i])] && !isSkippedDir(segs[i]) {
			return safeName(segs[i])
		}
	}
	name := safeName(source)
	if isSkippedDir(name) {
		name += " mod"
	}
	return name
}

var (
	reCopySuffix    = regexp.MustCompile(`\s*\(\d{1,3}\)$`)
	reVersionSuffix = regexp.MustCompile(`(?i)[\s_-]+v?\d+(\.\d+)+[a-z]?$|[\s_-]+v\d+$`)
)

// baseModName drops what downloads add to a mod's name: the browser's " (1)" for
// a second download, and a version ("-1.2.0", " v1.3"). "CoolCam-1.2.0 (1)" is CoolCam.
func baseModName(name string) string {
	b := strings.TrimSpace(reCopySuffix.ReplaceAllString(name, ""))
	b = strings.TrimSpace(reVersionSuffix.ReplaceAllString(b, ""))
	if b == "" {
		return name
	}
	return b
}

// scriptNames names the script mods found as plain folders in a download (or at
// its top, named after the download). The browser's " (1)" and a version are left
// out of the name, and an installed script mod with the same name is the older
// version of this one: a second download updates it instead of adding a second
// copy, which UE4SS would run as well.
func scriptNames(roots []string, source, ue string) map[string]string {
	names := map[string]string{}
	count := map[string]int{}
	for _, r := range roots {
		n := source
		if r != "" {
			n = path.Base(r)
		}
		names[r] = safeName(baseModName(n))
		count[strings.ToLower(names[r])]++
	}
	installed := listScriptMods(ue)
	for _, r := range roots {
		if count[strings.ToLower(names[r])] > 1 {
			// two mods in this download would get the same name: keep their own names
			n := source
			if r != "" {
				n = path.Base(r)
			}
			names[r] = safeName(n)
			continue
		}
		names[r] = installedScriptName(installed, names[r])
	}
	return names
}

// installedScriptName is the folder name of the installed script mod that name
// is a version of (the same name, or the same apart from a version or " (1)").
func installedScriptName(installed []*ScriptMod, name string) string {
	for _, m := range installed {
		if strings.EqualFold(m.Name, name) {
			return m.Name
		}
	}
	for _, m := range installed {
		if strings.EqualFold(baseModName(m.Name), name) {
			return m.Name
		}
	}
	return name
}

// UE4SS's own script mods. When a download brings them (often from another
// UE4SS version), the player's copy is kept, as UE4SS itself is.
var stockScriptMods = []string{"ActorDumperMod", "BPML_GenericFunctions", "BPModLoaderMod", "CheatManagerEnablerMod",
	"ConsoleCommandsMod", "ConsoleEnablerMod", "jsbLuaProfilerMod", "Keybinds", "LineTraceMod", "SplitScreenMod"}

func keepStockScriptMod(ue, name string) bool {
	for _, s := range stockScriptMods {
		if strings.EqualFold(s, name) {
			return isDir(join(ue, "Mods", name))
		}
	}
	return false
}

// isContainerFile: one of the files a pak mod is made of.
func isContainerFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".utoc", ".ucas", ".pak", ".sig":
		return true
	}
	return false
}

// skinStudioDirs are the folders of a download that hold a Skin Studio skin.
func skinStudioDirs(files []incoming) []string {
	var out []string
	for _, f := range files {
		if segs, ok := cleanSegments(f.rel); ok && strings.EqualFold(segs[len(segs)-1], "skinstudio.json") {
			out = append(out, strings.Join(segs[:len(segs)-1], "/"))
		}
	}
	return out
}

// inDirs: rel (with /) is inside one of dirs ("" is the whole download).
func inDirs(rel string, dirs []string) bool {
	for _, d := range dirs {
		if d == "" || strings.HasPrefix(strings.ToLower(rel), strings.ToLower(d)+"/") {
			return true
		}
	}
	return false
}

// joinAlternatives settles the units planned per folder above the place named in
// the download. Copies from several folders whose files have the same names are
// alternatives the player chooses between; otherwise they are one mod again.
func joinAlternatives(units map[string]*unit, order []string) (map[string]*unit, []string) {
	byBase := map[string][]*unit{}
	var seq []*unit // in order; a group sits where its first unit was
	for _, k := range order {
		u := units[k]
		if u.base == "" {
			seq = append(seq, u)
			continue
		}
		if len(byBase[u.base]) == 0 {
			seq = append(seq, u)
		}
		byBase[u.base] = append(byBase[u.base], u)
	}
	out := map[string]*unit{}
	var outOrder []string
	add := func(u *unit) {
		out[u.id] = u
		outOrder = append(outOrder, u.id)
	}
	for _, u := range seq {
		group := byBase[u.base]
		switch {
		case u.base == "":
			add(u)
		case len(group) == 1 || !filesCollide(group):
			for _, o := range group[1:] {
				for k, f := range o.files {
					u.files[k] = f
				}
			}
			u.id = u.base
			add(u)
		default:
			for _, o := range group {
				o.id = o.base + "@" + strings.ToLower(o.pre)
				o.alt = true
				add(o)
			}
		}
	}
	return out, outOrder
}

func filesCollide(group []*unit) bool {
	seen := map[string]*unit{}
	for _, u := range group {
		for k := range u.files {
			k = strings.ToLower(k)
			if o, ok := seen[k]; ok && o != u {
				return true
			}
			seen[k] = u
		}
	}
	return false
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
	if len(units) == 0 {
		for _, f := range files {
			if strings.EqualFold(path.Ext(f.rel), ".pak") && !isPakName(f.rel) {
				return res, errors.New(path.Base(f.rel) + " can't be loaded: the Mod Loader only loads .pak files whose names end in _P.pak. Ask the mod's author for a _P.pak version")
			}
		}
		for _, f := range files {
			switch strings.ToLower(path.Ext(f.rel)) {
			case ".zip", ".7z", ".rar":
				return res, errors.New("the download has another archive inside (" + path.Base(f.rel) + "), and no mod next to it. Unpack that archive, then add what comes out of it")
			}
		}
		return res, errors.New("no mod was found in " + source + ". Check that it is the mod's main download, not a preview or a readme. (A mod has .utoc and .ucas files, a file ending in _P.pak, or a Scripts\\main.lua file.)")
	}
	units, choices := pickVersions(units, pick, source)
	if len(choices) > 0 {
		res.Choices = choices
		return res, nil
	}
	installed, _ := scanPakMods(at.loader, at.tilde)
	if !loaderPaks(at.loader) {
		for _, u := range units {
			for _, n := range u.utocs {
				if isPakName(n) {
					res.Warnings = append(res.Warnings, u.name+" won't load yet: it is a .pak mod, and your Mod Loader is too old for those. Update the Mod Loader to 1.3.1 or newer in Settings > Modding setup.")
					break
				}
			}
		}
	}
	stage := join(g.Storage(), "Adding")
	os.RemoveAll(stage) // left over from a launcher that was closed while adding
	sweepUpdating(at.loader, join(at.ue, "Mods"), at.tilde, at.logic)
	defer os.RemoveAll(stage)
	var needsBPModLoader []string // the LogicMods mods added

	dated := map[string]bool{} // script mods: date key -> updated
	var datedPaks []struct {   // pak mods: where they went
		path    string
		updated bool
	}
	committed := map[string]bool{} // what this add has put in place (lower case)
	var replaced []replacement
	for i, u := range units {
		if len(u.files) == 0 {
			continue
		}
		// 1. Copy the new files next to the game first. Nothing in the game is
		// touched until every file of the download has been read.
		tmp := join(stage, strconv.Itoa(i))
		if err := stageUnit(u, tmp); err != nil {
			return res, fmt.Errorf("%s could not be added: %v", u.name, err)
		}
		if u.kind == "shared" {
			sr, err := g.commitShared(u, tmp, source)
			if err != nil {
				return res, fmt.Errorf("UE4SS's shared files could not be updated: %s Nothing was changed in the shared folder.", explainMoveError(err))
			}
			switch {
			case sr.Replaced > 0:
				res.Updated = append(res.Updated, fmt.Sprintf("UE4SS shared files (%s replaced, %s added)", plural(sr.Replaced, "file"), strconv.Itoa(sr.Added)))
			case sr.Added > 0:
				res.Added = append(res.Added, "UE4SS shared files ("+plural(sr.Added, "file")+")")
			}
			if len(sr.KeptStock) > 0 {
				res.Notes = append(res.Notes, "UE4SS's own shared files were kept as they are: "+strings.Join(sr.KeptStock, ", ")+".")
			}
			continue
		}
		uk := unitKeys(u)
		merge := false
		var notes []string // told once this mod is in
		if u.kind != "script" && !u.isFiles {
			var n string
			if merge, n = pickTarget(u, uk, committed); n != "" {
				notes = append(notes, n)
			}
			if merge {
				u.isFiles = true // the folder holds other mods too: only this mod's files go in
				for rel := range u.files {
					if !isContainerFile(rel) && lexists(join(u.target, filepath.FromSlash(rel))) {
						delete(u.files, rel) // the folder's own readme and such stay as they are
					}
				}
				if others := containerNames(u.target, uk); len(others) > 0 {
					notes = append(notes, u.name+" also holds other mods ("+strings.Join(others, ", ")+"). Only the files this download has were replaced; the others were kept.")
				}
			}
		}
		if u.kind != "script" {
			for _, n := range u.utocs {
				stem := strings.TrimSuffix(n, path.Ext(n))
				if isPakName(n) && !hasFileFold(u.files, stem+".utoc") {
					continue // a .pak on its own needs nothing else
				}
				if !isFile(join(u.target, stem+".ucas")) && !hasFileFold(u.files, stem+".ucas") {
					res.Warnings = append(res.Warnings, "Part of "+u.name+" is missing from the download ("+stem+".ucas), so it won't load. Download it again, or ask its author for the full files.")
				}
			}
		}
		// 2. An update replaces the installed copy in place; nothing goes to
		// Removed mods. The old files are first renamed aside where they are
		// ("<name>.ace8-updating") and only deleted once the new copy is in, so
		// a failed update leaves the old version working.
		var aside []asideItem
		var wholeOld []*PakMod
		if u.kind != "script" {
			var more []string
			aside, more, wholeOld = olderCopies(u, installed)
			notes = append(notes, more...)
		}
		replacedOlder := len(aside) > 0
		updated, wasOff := false, false
		switch {
		case u.isFiles:
			// loose files in ~mods or LogicMods (or a mod going into a folder that
			// holds other mods too): a file with the same name is an older copy
			for rel := range u.files {
				for _, p := range companions(join(u.target, filepath.FromSlash(rel))) {
					if lexists(p) {
						aside = append(aside, asideItem{path: p, what: filepath.Base(p)})
						updated = true
					}
				}
			}
			if merge {
				for _, p := range matchingFiles(u.target, overlap(folderKeys(u.target, false), uk)) {
					aside = append(aside, asideItem{path: p, what: filepath.Base(p), stop: u.target})
					updated = true
				}
			}
		case exists(u.target):
			updated = hasFiles(u.target)
			if u.kind == "script" {
				wasOff = scriptModOff(u.target, u.name)
				// keep what the player and other mods added to the installed copy:
				// files the new version doesn't have, and those ace8-keep.txt names
				carried, kept, err := carryOver(u.target, tmp)
				if err != nil {
					return res, fmt.Errorf("%s could not be updated: your files in it could not be kept (%v). Nothing was changed for this mod.", u.name, err)
				}
				if carried > 0 || kept > 0 {
					res.Notes = append(res.Notes, fmt.Sprintf("%s: kept %s of yours (settings and add-ons the new version doesn't have).", u.name, plural(carried+kept, "file")))
				}
				if wasOff {
					os.Remove(join(tmp, "enabled.txt")) // it stays switched off
				}
			}
			aside = append(aside, asideItem{path: u.target, what: u.name})
		}
		if touchesThisAdd(aside, committed) {
			// two parts of one download would replace each other: the first one stays
			res.Notes = append(res.Notes, u.name+" is in the download twice, so only its first copy was added.")
			continue
		}
		var asideMoves [][2]string // {where it was, where it is now}
		undo := func() {
			for k := len(asideMoves) - 1; k >= 0; k-- {
				if err := moveItem(asideMoves[k][1], asideMoves[k][0]); err != nil {
					logf("could not put %s back: %v", asideMoves[k][0], err)
				}
			}
		}
		seen := map[string]bool{}
		for _, a := range aside {
			if seen[strings.ToLower(a.path)] || !lexists(a.path) {
				continue
			}
			seen[strings.ToLower(a.path)] = true
			dst := a.path + updatingSuffix
			os.RemoveAll(dst)
			if err := moveItem(a.path, dst); err != nil {
				undo()
				return res, fmt.Errorf("could not replace %s: %s", a.what, explainMoveError(err))
			}
			asideMoves = append(asideMoves, [2]string{a.path, dst})
		}
		// 3. Move the new copy in. If that fails, the old one goes back. While a
		// folder goes in, a marker next to it says it is not all there yet, so a
		// launcher closed half way brings back the old copy, not half the new one.
		marker := ""
		if !u.isFiles && seen[strings.ToLower(u.target)] {
			marker = u.target + incompleteSuffix
			if os.WriteFile(marker, nil, 0o644) != nil {
				marker = ""
			}
		}
		if err := commitUnit(u, tmp); err != nil {
			undo()
			if marker != "" {
				os.Remove(marker)
			}
			return res, fmt.Errorf("%s could not be added: %s Nothing was changed for this mod.", u.name, explainMoveError(err))
		}
		if marker != "" {
			os.Remove(marker)
		}
		var newPaths []string
		if u.isFiles {
			for rel := range u.files {
				newPaths = append(newPaths, join(u.target, filepath.FromSlash(rel)))
			}
		} else {
			newPaths = []string{u.target}
		}
		for _, p := range newPaths {
			committed[strings.ToLower(filepath.Clean(p))] = true
		}
		// 4. The old files are no longer needed.
		for _, m := range asideMoves {
			if err := os.RemoveAll(m[1]); err != nil {
				logf("could not delete the old %s: %v", m[1], err)
			}
		}
		for _, a := range aside {
			if a.stop != "" {
				removeEmptyParents(filepath.Dir(a.path), a.stop) // a folder in a pack that only held the old copy
			}
		}
		res.Notes = append(res.Notes, notes...)
		if len(wholeOld) > 0 {
			replaced = append(replaced, replacement{old: wholeOld, paths: newPaths})
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
			needsBPModLoader = append(needsBPModLoader, u.name)
		}
		switch {
		case u.kind == "script":
			dated[scriptDateKey(u.name)] = updated
		case u.isFiles && !merge:
			for _, p := range newPaths {
				datedPaks = append(datedPaks, struct {
					path    string
					updated bool
				}{p, updated || replacedOlder})
			}
		default:
			datedPaks = append(datedPaks, struct {
				path    string
				updated bool
			}{u.target, updated || replacedOlder})
		}
		label := u.name
		if u.kind == "script" {
			label += " (script mod)"
		}
		if updated || replacedOlder {
			res.Updated = append(res.Updated, label)
		} else {
			res.Added = append(res.Added, label)
		}
	}
	if len(needsBPModLoader) > 0 && !isDir(join(at.ue, "Mods", "BPModLoaderMod")) {
		res.Warnings = append(res.Warnings, strings.Join(needsBPModLoader, ", ")+" won't load yet: LogicMods mods need UE4SS's BPModLoaderMod, which isn't installed. Add it from the full UE4SS download (its ue4ss\\Mods\\BPModLoaderMod folder).")
	}
	carried := keepReplacedPlaces(at, installed, replaced)
	g.tidyLoadOrder()
	if len(datedPaks) > 0 {
		now, _ := scanPakMods(at.loader, at.tilde)
		for _, m := range now {
			for _, d := range datedPaks {
				if samePath(m.Path, d.path) {
					dated[pakDateKey(m.Key)] = d.updated
				}
			}
		}
	}
	recordModDates(dated, carried)
	logf("added from %s: added %v, updated %v, notes %v, warnings %v", source, res.Added, res.Updated, res.Notes, res.Warnings)
	return res, nil
}

// versionChoices is the "Which version?" question a download needs, if any,
// without adding anything. When several downloads are added together, all of
// them are asked about first, so none is added before the player has chosen.
func (g Game) versionChoices(source string, files []incoming) []AddChoice {
	at, err := g.addPlaces()
	if err != nil {
		return nil
	}
	units, res, err := g.planAdd(source, files, at)
	if err != nil || res.SkinStudio || len(units) == 0 {
		return nil
	}
	_, ch := pickVersions(units, nil, source)
	return ch
}

// pickVersions offers a choice when a download holds several versions of one
// mod, and keeps only the one the player picked.
func pickVersions(units []*unit, pick []string, source string) ([]*unit, []AddChoice) {
	units, ch := pickVersionsIn(units, picksFor(pick, source), source)
	for i := range ch {
		ch[i].ID, ch[i].Source = source+"|"+ch[i].ID, source
	}
	return units, ch
}

// picksFor keeps the picks meant for this download, without the "<source>|"
// in front. nil when none are: nothing was picked for it, so a download
// without versions is added whole and one with versions is asked about.
func picksFor(pick []string, source string) []string {
	var mine []string
	for _, p := range pick {
		if strings.HasPrefix(p, source+"|") {
			mine = append(mine, strings.TrimPrefix(p, source+"|"))
		}
	}
	return mine
}

func pickVersionsIn(units []*unit, pick []string, source string) ([]*unit, []AddChoice) {
	want := map[string]bool{}
	for _, p := range pick {
		want[p] = true
	}
	keep := func(ok func(u *unit) bool) []*unit {
		var out []*unit
		for _, u := range units {
			if ok(u) {
				out = append(out, u)
			}
		}
		return out
	}
	// the same files from several folders of the download ("Option A\~mods\…", "Option B\~mods\…")
	if ch := altChoices(units, source); len(ch) > 0 {
		if pick == nil {
			return units, ch
		}
		return keep(func(u *unit) bool { return !u.alt || want["from:"+strings.ToLower(u.pre)] }), nil
	}
	// several mod folders that change the same game files, or have the same file names
	var cands []*unit
	for _, u := range units {
		if choosable(u) {
			cands = append(cands, u)
		}
	}
	if pick == nil {
		if len(cands) < 2 || !looksLikeVersions(cands) {
			return units, nil
		}
		var ch []AddChoice
		for _, u := range cands {
			var fl []string
			for n := range u.files {
				fl = append(fl, n)
			}
			sort.Strings(fl)
			ch = append(ch, AddChoice{ID: u.id, Name: u.name, Files: fl})
		}
		return units, ch
	}
	kept := keep(func(u *unit) bool { return !choosable(u) || want[u.id] })
	for _, u := range kept {
		if choosable(u) && want[u.id] {
			nameAfterDownload(u, source)
		}
	}
	return kept, nil
}

// nameAfterDownload names a version picked from a download after the download
// too ("Su-57 Liveries - Red", not just "Red"), so it can be told apart from
// other mods' versions. The same version from the same download (or a newer
// one) gets the same name again, so adding it again updates it.
func nameAfterDownload(u *unit, source string) {
	base := baseModName(source)
	if base == "" || strings.Contains(strings.ToLower(u.name), strings.ToLower(base)) {
		return
	}
	name := safeName(base + " - " + u.name)
	if isSkippedDir(name) {
		return
	}
	u.name, u.target = name, join(filepath.Dir(u.target), name)
}

// choosable: a mod folder that can be one of several versions in a download.
// Loose files in ~mods or LogicMods are not: several of them in one download
// are usually parts of one mod, like the files of one folder.
func choosable(u *unit) bool {
	return (u.kind == "pak" || u.kind == "tilde" || u.kind == "logic") && !u.alt && len(unitKeys(u)) > 0
}

// altChoices: one choice per folder of the download that holds a version.
func altChoices(units []*unit, source string) []AddChoice {
	var pres []string
	files := map[string][]string{}
	for _, u := range units {
		if !u.alt {
			continue
		}
		k := strings.ToLower(u.pre)
		if _, ok := files[k]; !ok {
			pres = append(pres, u.pre)
		}
		for n := range u.files {
			files[k] = append(files[k], n)
		}
	}
	names := map[string]int{}
	for _, p := range pres {
		names[strings.ToLower(path.Base(p))]++
	}
	var out []AddChoice
	for _, p := range pres {
		name := path.Base(p)
		switch {
		case p == "":
			name = source
		case names[strings.ToLower(name)] > 1:
			name = strings.ReplaceAll(p, "/", " - ")
		}
		fl := files[strings.ToLower(p)]
		sort.Strings(fl)
		out = append(out, AddChoice{ID: "from:" + strings.ToLower(p), Name: name, Files: fl})
	}
	return out
}

// asideItem is something an update sets aside: where it is, how to name it in a
// message, and (for files taken out of a folder that stays) that folder.
type asideItem struct{ path, what, stop string }

// replacement: older copies of a mod that were replaced, as a whole, by the new
// copy at paths.
type replacement struct {
	old   []*PakMod
	paths []string
}

// unitKeys are the names (as containerKey) of every .utoc, .ucas, .pak and .sig
// file of a unit.
func unitKeys(u *unit) map[string]bool {
	out := map[string]bool{}
	for n := range u.files {
		if isContainerFile(n) {
			out[containerKey(path.Base(n))] = true
		}
	}
	return out
}

// folderKeys are the names (as containerKey) of the .utoc, .ucas, .pak and .sig
// files in a folder; with skipped, also those in folders the Mod Loader skips
// (an "old" or "backup" folder the player made).
func folderKeys(dir string, skipped bool) map[string]bool {
	out := map[string]bool{}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		switch {
		case err != nil:
			return nil
		case info.IsDir():
			if !skipped && p != dir && isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
		case isContainerFile(info.Name()):
			out[containerKey(info.Name())] = true
		}
		return nil
	})
	return out
}

// matchingFiles are the files of the containers named in keys, in dir and the
// folders in it that the Mod Loader reads.
func matchingFiles(dir string, keys map[string]bool) []string {
	var out []string
	if len(keys) == 0 {
		return nil
	}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		switch {
		case err != nil:
			return nil
		case info.IsDir():
			if p != dir && isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
		case isContainerFile(info.Name()) && keys[containerKey(info.Name())]:
			out = append(out, p)
		}
		return nil
	})
	return out
}

// containerNames are the mods (.utoc and *_P.pak names) in dir that are not in keys.
func containerNames(dir string, keys map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		switch {
		case err != nil:
			return nil
		case info.IsDir():
			if p != dir && isSkippedDir(info.Name()) {
				return filepath.SkipDir
			}
		case strings.HasSuffix(strings.ToLower(info.Name()), ".utoc") || isPakName(info.Name()):
			if k := containerKey(info.Name()); !keys[k] && !seen[k] {
				seen[k] = true
				out = append(out, strings.TrimSuffix(info.Name(), filepath.Ext(info.Name())))
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func overlap(a, b map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k := range a {
		if b[k] {
			out[k] = true
		}
	}
	return out
}

// keysWithin: every key of a is in b.
func keysWithin(a, b map[string]bool) bool {
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// pickTarget decides where a mod folder goes when a folder with its name is
// already there. It is an update of that folder only when the folder holds at
// least one of the same .utoc/.ucas/.pak names, that is when it is plausibly the
// same mod. A different mod that only has the same folder name (often a generic
// one, "Paks" or "New mod") is never replaced: the new one goes to "Name (2)".
// When the folder holds other mods as well (a pack, or a folder the player made),
// merge is true: only the matching files in it are replaced and the rest stays.
func pickTarget(u *unit, uk map[string]bool, committed map[string]bool) (merge bool, note string) {
	parent, first := filepath.Dir(u.target), u.name
	for n := 1; n < 1000; n++ {
		name := first
		if n > 1 {
			name = fmt.Sprintf("%s (%d)", first, n)
		}
		t := join(parent, name)
		if committed[strings.ToLower(filepath.Clean(t))] {
			continue // another part of this download went there
		}
		free := !lexists(t) || (isDir(t) && !hasFiles(t))
		if !free && len(overlap(folderKeys(t, false), uk)) == 0 {
			continue
		}
		if n > 1 {
			note = "A different mod called " + first + " is already installed, so this one was added as " + name + ". If it is in fact a newer version of " + first + ", remove the old one on the Mods page."
		}
		u.name, u.target = name, t
		return !free && !keysWithin(folderKeys(t, true), uk), note
	}
	return false, ""
}

// olderCopies finds this mod installed under another name: a mod elsewhere that
// has one of the unit's containers. A mod whose files the new download all has
// is replaced as a whole. A folder that holds other mods as well (a pack, or a
// folder the player made, such as "~mods\Skins") keeps them: only the matching
// containers' files in it are replaced, never the whole folder.
func olderCopies(u *unit, installed []*PakMod) (aside []asideItem, notes []string, whole []*PakMod) {
	ck := map[string]bool{}
	for _, n := range u.utocs {
		ck[containerKey(n)] = true
	}
	uk := unitKeys(u)
	seen := map[string]bool{}
	for _, m := range installed {
		if samePath(m.Path, u.target) || seen[strings.ToLower(m.Path)] || !exists(m.Path) {
			continue
		}
		if u.isFiles && m.IsFile && samePath(filepath.Dir(m.Path), u.target) {
			continue // a loose file with the same name: replaced with the unit's own files
		}
		matched := map[string]bool{}
		var names []string
		for _, c := range m.Containers {
			if k := containerKey(c.Name); ck[k] && !matched[k] {
				matched[k] = true
				names = append(names, strings.TrimSuffix(c.Name, filepath.Ext(c.Name)))
			}
		}
		if len(matched) == 0 {
			continue
		}
		seen[strings.ToLower(m.Path)] = true
		what := "the older copy in " + m.Key
		switch {
		case m.IsFile:
			for _, p := range companions(m.Path) {
				aside = append(aside, asideItem{path: p, what: what})
			}
		case keysWithin(folderKeys(m.Path, true), uk):
			aside = append(aside, asideItem{path: m.Path, what: what})
		default:
			for _, p := range matchingFiles(m.Path, matched) {
				aside = append(aside, asideItem{path: p, what: what, stop: m.Path})
			}
			notes = append(notes, "The older copy of "+strings.Join(names, ", ")+" in "+m.Key+" was replaced. The other mods in "+m.Key+" were kept.")
			continue
		}
		whole = append(whole, m)
		notes = append(notes, "The older copy in "+m.Key+" was replaced.")
	}
	return aside, notes, whole
}

// companions: a loose mod is up to four files with the same name; they go together.
func companions(p string) []string {
	ext := strings.ToLower(filepath.Ext(p))
	if ext != ".utoc" && ext != ".ucas" && ext != ".pak" && ext != ".sig" {
		return []string{p}
	}
	base := strings.TrimSuffix(p, filepath.Ext(p))
	var out []string
	for _, e := range []string{".utoc", ".ucas", ".pak", ".sig"} {
		out = append(out, base+e)
	}
	return out
}

// touchesThisAdd: something to be set aside is (or holds, or is inside) what an
// earlier part of the same add has just put in place.
func touchesThisAdd(aside []asideItem, committed map[string]bool) bool {
	sep := string(filepath.Separator)
	for _, a := range aside {
		p := strings.ToLower(filepath.Clean(a.path))
		for c := range committed {
			if c == p || strings.HasPrefix(c, p+sep) || strings.HasPrefix(p, c+sep) {
				return true
			}
		}
	}
	return false
}

// scriptModOff: the installed script mod is switched off, by the rule UE4SS uses
// and the Script mods list shows: "Name : 0" in mods.txt, or no enabled.txt
// unless mods.txt says "Name : 1".
func scriptModOff(dir, name string) bool {
	on, listed := modsTxtState(filepath.Dir(dir))[strings.ToLower(name)]
	return !((listed && on) || (isFile(join(dir, "enabled.txt")) && !(listed && !on)))
}

// keepReplacedPlaces gives an updated mod whose folder has another name now
// ("Hornet v1" became "Hornet v2") the old one's place in the load order and its
// on/off state, so an update never switches a mod back on or makes it win
// conflicts it used to lose. It returns, for the mod dates, new key -> old key.
func keepReplacedPlaces(at addPlaces, before []*PakMod, reps []replacement) map[string]string {
	carried := map[string]string{}
	if len(reps) == 0 || !isDir(at.loader) {
		return carried
	}
	now, _ := scanPakMods(at.loader, at.tilde)
	newKey := map[string]string{} // old key (lower case) -> new key
	for _, r := range reps {
		key := ""
		for _, m := range now {
			for _, p := range r.paths {
				if key == "" && samePath(m.Path, p) {
					key = m.Key
				}
			}
		}
		if key == "" {
			continue
		}
		for _, o := range r.old {
			newKey[strings.ToLower(o.Key)] = key
			if _, ok := carried[pakDateKey(key)]; !ok {
				carried[pakDateKey(key)] = pakDateKey(o.Key)
			}
		}
	}
	if len(newKey) == 0 {
		return carried
	}
	var entries []orderEntry
	placed := map[string]bool{}
	for _, m := range before {
		key := m.Key
		if k, ok := newKey[strings.ToLower(m.Key)]; ok {
			key = k
		}
		if placed[strings.ToLower(key)] {
			continue
		}
		placed[strings.ToLower(key)] = true
		entries = append(entries, orderEntry{Key: key, Enabled: m.Enabled})
	}
	if err := writeLoadOrder(at.loader, keepSkinListLast(entries)); err != nil {
		logf("could not keep the load order place of an updated mod: %v", err)
	}
	return carried
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

// looksLikeVersions: several mod folders in one download are usually alternatives
// ("Blue", "Red") when they replace the same game files, or have the same file names.
func looksLikeVersions(paks []*unit) bool {
	names := map[string]int{}
	for _, u := range paks {
		for k := range unitKeys(u) {
			names[k]++
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
	Kind  string   `json:"kind"`
	Name  string   `json:"name"`
	From  string   `json:"from"`
	Files []string `json:"files,omitempty"` // for a mod that was loose files
	// Rel is From inside the game folder ("Game/Content/Paks/~mods/…"), so the
	// mod still finds its way back after the game or the storage folder moved.
	Rel     string    `json:"rel,omitempty"`
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
	if rel, ok := g.gameRel(p); ok {
		ri.Rel = filepath.ToSlash(rel)
	}
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
// in the meantime, it goes where that folder is now: into the game while
// Modded, into "Switched off" while Unmodded. It goes back whole or not at all.
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
	if g.modeInfo().Mode == modeMixed {
		return errors.New("the last switch did not finish. Press Modded or Unmodded on the Play page first, then restore the mod")
	}
	if ri.Kind == "shared" {
		if err := g.restoreShared(dir, ri); err != nil {
			return err
		}
		logf("restored %s", ri.Name)
		return nil
	}
	from := ri.From
	if ri.Rel != "" {
		from = join(g.Root, filepath.FromSlash(ri.Rel))
	}
	to := g.relocate(from)
	if !isInside(to, g.Root) && !isInside(to, g.OffDir()) {
		return fmt.Errorf("the launcher can't tell where %s goes back to: it came from %s, which is not part of this game folder. Its files are kept in %s", ri.Name, filepath.Dir(ri.From), dir)
	}
	type move struct{ from, to string }
	var moves []move
	if len(ri.Files) > 0 {
		// a loose mod: its files go back next to each other
		for _, f := range ri.Files {
			if src := join(dir, filepath.Base(f)); lexists(src) { // not there: put back by an earlier try that stopped part way
				moves = append(moves, move{src, join(filepath.Dir(to), filepath.Base(f))})
			}
		}
	} else if src := join(dir, filepath.Base(ri.From)); lexists(src) {
		moves = append(moves, move{src, to})
	}
	if len(moves) == 0 {
		return fmt.Errorf("the files of %s are no longer in the storage folder (%s)", ri.Name, dir)
	}
	for _, m := range moves {
		if lexists(m.to) {
			return errors.New(filepath.Base(m.to) + " is installed again already. Remove that one first if you want this copy back")
		}
	}
	for i, m := range moves {
		if err := moveItem(m.from, m.to); err != nil {
			// put back what already moved, so the mod is never left half restored
			back := " Nothing was changed."
			for k := i - 1; k >= 0; k-- {
				if berr := moveItem(moves[k].to, moves[k].from); berr != nil {
					logf("restore: could not put %s back: %v", moves[k].to, berr)
					back = " Part of it is back in the game already; remove it from the Mods page and try again."
				}
			}
			return fmt.Errorf("%s would not move %s back into %s. %s%s", sysName, filepath.Base(m.from), filepath.Dir(m.to), explainMoveError(err), back)
		}
	}
	os.Remove(join(dir, "removed.json"))
	os.Remove(dir)
	if ri.Kind == "pak" || ri.Kind == "tilde" {
		g.tidyLoadOrder()
	}
	logf("restored %s to %s", ri.Name, to)
	return nil
}

// relocate maps a path inside the game, or inside its switched-off copy, to
// where it goes now. A folder that has files on one side only stays on that
// side. When it has none (the last mod in ~mods was removed and the empty
// folder tidied away), the mode decides, as for adding a mod: Unmodded puts it
// in "Switched off", Modded in the game.
func (g Game) relocate(p string) string {
	rel, ok := g.gameRel(p)
	if !ok {
		return p
	}
	inGameAt := join(g.Root, rel)
	for _, it := range parkItems {
		gp, pp := g.itemPaths(it)
		r, inside := relInside(inGameAt, gp)
		if !inside || r == "" {
			continue
		}
		inGame, parked := hasFiles(gp), hasFiles(pp)
		switch {
		case parked && !inGame:
			return join(pp, r)
		case inGame && !parked:
			return join(gp, r)
		case !inGame && !parked:
			if g.modeInfo().Mode == modeUnmodded {
				return join(pp, r)
			}
			return join(gp, r)
		}
		// on both sides (a switch that did not finish): the side it came from
		if _, wasOff := relInside(p, g.OffDir()); wasOff {
			return join(pp, r)
		}
		return join(gp, r)
	}
	return inGameAt
}

// gameRel is p as a path inside the game folder ("Game\Content\Paks\~mods\…"),
// for a path in the game or in its switched-off copy. A path saved before the
// game or the storage folder moved is recognised by its "Game\Binaries" or
// "Game\Content" part.
func (g Game) gameRel(p string) (string, bool) {
	for _, base := range []string{g.Root, g.OffDir()} {
		if r, ok := relInside(p, base); ok && r != "" && base != "" {
			return r, true
		}
	}
	segs := strings.Split(filepath.Clean(p), string(filepath.Separator))
	for i := 0; i+1 < len(segs); i++ {
		if strings.EqualFold(segs[i], "Game") && (strings.EqualFold(segs[i+1], "Binaries") || strings.EqualFold(segs[i+1], "Content")) {
			return filepath.Join(segs[i:]...), true
		}
	}
	return "", false
}

func (g Game) deleteRemoved(id string) error {
	dir, _, err := g.removedEntry(id)
	if err != nil {
		return err
	}
	logf("deleted removed mod %s", dir)
	return os.RemoveAll(dir)
}
