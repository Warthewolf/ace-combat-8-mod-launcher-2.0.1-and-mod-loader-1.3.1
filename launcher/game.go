package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Game is an Ace Combat 8 install: the folder that holds "Game", "Engine" and
// "EasyAntiCheat" (…\steamapps\common\ACE COMBAT 8).
type Game struct {
	Root string
}

func join(base string, parts ...string) string {
	return filepath.Join(append([]string{base}, parts...)...)
}

func (g Game) Win64() string   { return join(g.Root, "Game", "Binaries", "Win64") }
func (g Game) GameExe() string { return join(g.Win64(), "AceCombat8.exe") }
func (g Game) Paks() string    { return join(g.Root, "Game", "Content", "Paks") }
func (g Game) EACDir() string  { return join(g.Root, "EasyAntiCheat") }
func (g Game) Bootstrapper() string {
	return join(g.Root, "start_protected_game.exe")
}

// Storage is the folder where switched-off and removed mods are kept: the one
// the player chose in Settings, or by default the folder next to the game. Next
// to the game it is on the same drive, so moving things there is instant, and it
// is outside the game folder, so Steam and the game never see it.
func (g Game) Storage() string {
	if s := getSettings(); s.StorageDir != "" && samePath(s.StorageGame, g.Root) {
		return filepath.Clean(s.StorageDir)
	}
	return g.DefaultStorage()
}

func (g Game) DefaultStorage() string {
	return filepath.Join(filepath.Dir(g.Root), g.storageName())
}

func (g Game) storageName() string { return filepath.Base(g.Root) + " - mod storage" }

// holdsMods: the storage folder has switched-off or removed mods in it.
func holdsMods(storage string) bool {
	return hasFiles(join(storage, "Switched off")) || hasFiles(join(storage, "Removed mods"))
}

// keepStorageAfterMove is called when the game folder changes from oldRoot to
// newRoot. When the game was moved (Steam's "Move install folder", a reinstall
// to another library), the storage folder does not move with it. Without this,
// the launcher would start with an empty storage folder next to the new place,
// and every switched-off and removed mod would seem to be gone. So the storage
// folder of the old place stays in use; "Use the default" in Settings moves it
// next to the game. A game folder that is still there is another install, and
// keeps its own storage folder.
func keepStorageAfterMove(s *Settings, oldRoot, newRoot string) {
	if oldRoot == "" || samePath(oldRoot, newRoot) || looksLikeGame(oldRoot) {
		return
	}
	if s.StorageDir != "" && samePath(s.StorageGame, oldRoot) {
		if isDir(s.StorageDir) && !isInside(s.StorageDir, newRoot) {
			s.StorageGame = newRoot
			logf("the game moved from %s to %s; the chosen storage folder %s stays in use", oldRoot, newRoot, s.StorageDir)
		}
		return
	}
	if s.StorageDir != "" && samePath(s.StorageGame, newRoot) {
		return // a folder chosen for the new place already
	}
	oldDefault := Game{Root: oldRoot}.DefaultStorage()
	if holdsMods(oldDefault) && !holdsMods(Game{Root: newRoot}.DefaultStorage()) && !isInside(oldDefault, newRoot) {
		s.StorageDir, s.StorageGame = oldDefault, newRoot
		logf("the game moved from %s to %s; its storage folder %s stays in use", oldRoot, newRoot, oldDefault)
	}
}

// customStorage: the player chose the storage folder.
func (g Game) customStorage() bool { return !samePath(g.Storage(), g.DefaultStorage()) }

// storageMissing: a storage folder the player chose can't be found (a drive
// that is not connected, or a folder that was moved or deleted).
func (g Game) storageMissing() bool { return g.customStorage() && !isDir(g.Storage()) }
func (g Game) OffDir() string       { return join(g.Storage(), "Switched off") }
func (g Game) RemovedDir() string   { return join(g.Storage(), "Removed mods") }

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// looksLikeGame reports whether root is an Ace Combat 8 install.
func looksLikeGame(root string) bool {
	return isFile(join(root, "Game", "Binaries", "Win64", "AceCombat8.exe"))
}

// expandHome turns a typed "~/…" into the home folder.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

var errNotGame = errors.New("this folder is not Ace Combat 8: AceCombat8.exe was not found in it")

// normalizeGameDir accepts the game folder itself, any folder inside it, the
// folder that contains it (steamapps\common) or AceCombat8.exe, and returns the
// game folder.
func normalizeGameDir(p string) (string, error) {
	p = expandHome(strings.TrimSpace(strings.Trim(strings.TrimSpace(p), `"'`)))
	if p == "" {
		return "", errNotGame
	}
	// a folder typed without its full path is taken from where the launcher
	// runs now, and saved in full, so it still means the same at the next start
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	if isFile(p) {
		p = filepath.Dir(p)
	}
	for d, n := p, 0; n < 8; n++ {
		if looksLikeGame(d) {
			return d, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	for _, sub := range []string{"ACE COMBAT 8", filepath.Join("common", "ACE COMBAT 8"), filepath.Join("steamapps", "common", "ACE COMBAT 8")} {
		if looksLikeGame(join(p, sub)) {
			return join(p, sub), nil
		}
	}
	return "", errNotGame
}

// ---- Steam -------------------------------------------------------------------

// steamDir finds Steam's own folder (the one with steam.exe and "userdata").
func steamDir() string {
	var found []string
	for _, p := range steamDirCandidates() {
		if p != "" && (isFile(join(p, "steam.exe")) || isDir(join(p, "steamapps"))) {
			p = filepath.Clean(p)
			dup := false
			for _, f := range found {
				dup = dup || samePath(f, p)
			}
			if !dup {
				found = append(found, p)
			}
		}
	}
	switch {
	case len(found) == 0:
		return ""
	case len(found) == 1 || platform != "linux":
		return found[0] // Windows: the registry names the Steam that is installed
	}
	return pickSteamDir(found)
}

// pickSteamDir chooses between several Steam folders on Linux. Moving from the
// normal Steam to the Flatpak (or Snap) one leaves the old folder behind, and
// reading or changing the launch options there does nothing. The Steam whose
// libraries have the game wins, then the one whose settings were saved last.
func pickSteamDir(found []string) string {
	gameDir := getSettings().GameDir
	best, bestHas, bestTime := "", false, time.Time{}
	for _, sd := range found {
		has := false
		for _, lib := range steamLibrariesOf(sd) {
			if (gameDir != "" && isInside(gameDir, lib)) || isFile(join(lib, "steamapps", "appmanifest_"+steamAppID+".acf")) {
				has = true
			}
		}
		var newest time.Time
		users, _ := os.ReadDir(join(sd, "userdata"))
		for _, u := range users {
			if st, err := os.Stat(join(sd, "userdata", u.Name(), "config", "localconfig.vdf")); err == nil && st.ModTime().After(newest) {
				newest = st.ModTime()
			}
		}
		if best == "" || (has && !bestHas) || (has == bestHas && newest.After(bestTime)) {
			best, bestHas, bestTime = sd, has, newest
		}
	}
	return best
}

func steamDirCandidates() []string {
	var c []string
	c = append(c, registrySteamPaths()...)
	if v := os.Getenv("ACE8_FAKE_STEAM"); v != "" {
		c = append(filepath.SplitList(v), c...) // tests: one or more folders
	}
	if runtime.GOOS == "windows" {
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
			if b := os.Getenv(env); b != "" {
				c = append(c, join(b, "Steam"))
			}
		}
	}
	return c
}

// steamLibraries lists every Steam library folder (each has a "steamapps" folder).
func steamLibraries() []string { return steamLibrariesOf(steamDir()) }

// steamLibrariesOf lists the library folders of the Steam in sd.
func steamLibrariesOf(sd string) []string {
	var libs []string
	seen := map[string]bool{}
	add := func(p string) {
		p = filepath.Clean(p)
		k := strings.ToLower(p)
		if p != "" && !seen[k] && isDir(join(p, "steamapps")) {
			seen[k] = true
			libs = append(libs, p)
		}
	}
	if sd != "" {
		add(sd)
		if b, err := os.ReadFile(join(sd, "steamapps", "libraryfolders.vdf")); err == nil {
			if root, err := parseVDF(string(b)); err == nil {
				if lf := root.child("libraryfolders"); lf != nil {
					for _, k := range lf.keys {
						if n := lf.child(k); n != nil {
							if p := n.value("path"); p != "" {
								add(p)
							}
						}
					}
				}
			}
		}
	}
	return libs
}

// detectGame looks for Ace Combat 8 in Steam's libraries, then in the usual places.
func detectGame() string {
	for _, lib := range steamLibraries() {
		dir := "ACE COMBAT 8"
		if b, err := os.ReadFile(join(lib, "steamapps", "appmanifest_"+steamAppID+".acf")); err == nil {
			if root, err := parseVDF(string(b)); err == nil {
				if st := root.child("AppState"); st != nil && st.value("installdir") != "" {
					dir = st.value("installdir")
				}
			}
		}
		if p := join(lib, "steamapps", "common", dir); looksLikeGame(p) {
			return p
		}
	}
	// Windows: the usual places on each local drive (C: to Z:). Network and
	// CD/DVD drives are left out: one that is offline or empty can take seconds
	// to answer.
	for _, d := range driveRoots() {
		for _, rel := range []string{`SteamLibrary\steamapps\common\ACE COMBAT 8`, `Steam\steamapps\common\ACE COMBAT 8`,
			`Program Files (x86)\Steam\steamapps\common\ACE COMBAT 8`, `Games\SteamLibrary\steamapps\common\ACE COMBAT 8`,
			`Games\Steam\steamapps\common\ACE COMBAT 8`} {
			if p := d + rel; looksLikeGame(p) {
				return p
			}
		}
	}
	return ""
}

var (
	detectMu     sync.Mutex
	lastNotFound time.Time

	// foundElsewhere: a copy of the game found while the chosen game folder
	// can't be found (its drive is not connected); the player decides
	foundMu        sync.Mutex
	foundElsewhere string
)

// gameDirUnplugged: the game folder in the settings can't be found, and
// neither can the folder it was in. That is a drive that is not connected
// (or a library that is gone), not a game Steam moved or uninstalled, and
// another copy found meanwhile must not take its place for good.
func gameDirUnplugged(dir string) bool {
	return dir != "" && !looksLikeGame(dir) && !isDir(filepath.Dir(dir))
}

// currentGame returns the game folder from the settings, finding it the first time.
func currentGame() (Game, bool) {
	s := getSettings()
	if s.GameDir != "" && looksLikeGame(s.GameDir) {
		return Game{Root: s.GameDir}, true
	}
	// Looking through every drive takes a moment (longer with a sleeping
	// drive), and the window asks every two seconds: look again at most every
	// 20 s. Only one request looks; the others don't wait for it, so the
	// window keeps answering and "Choose the game folder" still works.
	if !detectMu.TryLock() {
		return Game{}, false
	}
	defer detectMu.Unlock()
	if s = getSettings(); s.GameDir != "" && looksLikeGame(s.GameDir) {
		return Game{Root: s.GameDir}, true // chosen meanwhile
	}
	if time.Since(lastNotFound) < 20*time.Second {
		return Game{}, false
	}
	p, other := detectGame(), ""
	if p != "" && gameDirUnplugged(s.GameDir) {
		p, other = "", p
	}
	foundMu.Lock()
	if other != "" && other != foundElsewhere {
		logf("the game folder %s can't be found; there is another copy at %s", s.GameDir, other)
	}
	foundElsewhere = other
	foundMu.Unlock()
	if p != "" {
		change := func(s *Settings) {
			keepStorageAfterMove(s, s.GameDir, p)
			s.GameDir = p
		}
		if err := updateSettings(change); err != nil {
			logf("could not save the game folder: %v", err)
			settingsMu.Lock()
			change(&settings) // still use it now; it is looked for again at the next start
			settingsMu.Unlock()
		}
		logf("found the game at %s", p)
		return Game{Root: p}, true
	}
	lastNotFound = time.Now()
	return Game{}, false
}

// gameMissing describes why the game can't be found: the chosen folder that is
// gone ("" when none was chosen or found yet) and a copy found elsewhere.
func gameMissing() (missing, elsewhere string) {
	s := getSettings()
	if s.GameDir == "" || looksLikeGame(s.GameDir) {
		return "", ""
	}
	foundMu.Lock()
	elsewhere = foundElsewhere
	foundMu.Unlock()
	if elsewhere != "" && !looksLikeGame(elsewhere) {
		elsewhere = ""
	}
	return s.GameDir, elsewhere
}

// errNoGame: an action needs the game, and it is not known.
func errNoGame() error {
	if missing, _ := gameMissing(); missing != "" {
		return fmt.Errorf("the game folder (%s) can't be found. Connect the drive it is on, or choose the game folder again in Settings", missing)
	}
	return errors.New("the launcher does not know where Ace Combat 8 is yet. Choose the game folder first")
}

// steamLaunchOptions returns the launch options set for the game in Steam, for
// each Steam account on this PC that has some ("" when none are set).
func steamLaunchOptions() []string {
	sd := steamDir()
	if sd == "" {
		return nil
	}
	users, _ := os.ReadDir(join(sd, "userdata"))
	var out []string
	for _, u := range users {
		if !u.IsDir() {
			continue
		}
		b, err := os.ReadFile(join(sd, "userdata", u.Name(), "config", "localconfig.vdf"))
		if err != nil {
			continue
		}
		root, err := parseVDF(string(b))
		if err != nil {
			continue
		}
		n := root.path("UserLocalConfigStore", "Software", "Valve", "Steam", "apps", steamAppID)
		if n == nil {
			n = root.path("UserLocalConfigStore", "Software", "valve", "Steam", "apps", steamAppID)
		}
		if n != nil {
			if v := n.value("LaunchOptions"); strings.TrimSpace(v) != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

// ---- a small reader for Valve's text "KeyValues" files (.vdf, .acf) ---------

type vdfNode struct {
	keys     []string
	values   map[string]string
	children map[string]*vdfNode
}

func newVDF() *vdfNode {
	return &vdfNode{values: map[string]string{}, children: map[string]*vdfNode{}}
}

func (n *vdfNode) child(k string) *vdfNode {
	if n == nil {
		return nil
	}
	if c, ok := n.children[k]; ok {
		return c
	}
	for kk, c := range n.children {
		if strings.EqualFold(kk, k) {
			return c
		}
	}
	return nil
}

func (n *vdfNode) value(k string) string {
	if n == nil {
		return ""
	}
	if v, ok := n.values[k]; ok {
		return v
	}
	for kk, v := range n.values {
		if strings.EqualFold(kk, k) {
			return v
		}
	}
	return ""
}

func (n *vdfNode) path(keys ...string) *vdfNode {
	for _, k := range keys {
		n = n.child(k)
		if n == nil {
			return nil
		}
	}
	return n
}

func parseVDF(s string) (*vdfNode, error) {
	root := newVDF()
	stack := []*vdfNode{root}
	i := 0
	readTok := func() (string, bool, bool) { // token, quoted, ok
		for i < len(s) {
			c := s[i]
			if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				i++
				continue
			}
			if c == '/' && i+1 < len(s) && s[i+1] == '/' {
				for i < len(s) && s[i] != '\n' {
					i++
				}
				continue
			}
			break
		}
		if i >= len(s) {
			return "", false, false
		}
		if s[i] == '{' || s[i] == '}' {
			i++
			return string(s[i-1]), false, true
		}
		if s[i] == '"' {
			i++
			var b strings.Builder
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
					switch s[i] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(s[i])
					}
					i++
					continue
				}
				b.WriteByte(s[i])
				i++
			}
			i++
			return b.String(), true, true
		}
		st := i
		for i < len(s) && !strings.ContainsRune(" \t\r\n{}\"", rune(s[i])) {
			i++
		}
		return s[st:i], false, true
	}
	var pendingKey string
	havePending := false
	for {
		tok, quoted, ok := readTok()
		if !ok {
			break
		}
		cur := stack[len(stack)-1]
		switch {
		case !quoted && tok == "{":
			if !havePending {
				return nil, errors.New("vdf: block without a name")
			}
			n := newVDF()
			if _, dup := cur.children[pendingKey]; !dup {
				cur.keys = append(cur.keys, pendingKey)
			}
			cur.children[pendingKey] = n
			stack = append(stack, n)
			havePending = false
		case !quoted && tok == "}":
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
			havePending = false
		case havePending:
			cur.values[pendingKey] = tok
			havePending = false
		default:
			pendingKey, havePending = tok, true
		}
	}
	return root, nil
}
