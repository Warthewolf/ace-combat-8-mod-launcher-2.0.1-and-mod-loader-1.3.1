package main

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

//go:embed ui
var uiFiles embed.FS

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	writeJSON(w, 200, map[string]any{"ok": false, "error": sentence(err.Error())})
}

// sentence starts a message with a capital letter and ends it with a full stop.
// The first letter is a whole letter, so a mod name such as "機体スキン" or
// "émile" at the start stays readable.
func sentence(msg string) string {
	if msg == "" {
		return msg
	}
	if r, n := utf8.DecodeRuneInString(msg); r != utf8.RuneError {
		msg = string(unicode.ToUpper(r)) + msg[n:]
	}
	if !strings.HasSuffix(msg, ".") && !strings.HasSuffix(msg, ")") {
		msg += "."
	}
	return msg
}

// uiTypes are the types of the window's own files. Windows lets other
// programs change what a file ending means (in the registry); a .css taken
// for plain text would leave the window without its layout.
var uiTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
	".png":  "image/png",
	".svg":  "image/svg+xml",
	".woff": "font/woff",
	".txt":  "text/plain; charset=utf-8",
}

func clientID(r *http.Request) string {
	if c := r.Header.Get("X-Client"); c != "" {
		return c
	}
	return r.URL.Query().Get("c")
}

func newMux(port int) http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(uiFiles, "ui")
	allowedHosts := map[string]bool{fmt.Sprintf("127.0.0.1:%d", port): true, fmt.Sprintf("localhost:%d", port): true}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		b, err := fs.ReadFile(static, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if ct, ok := uiTypes[strings.ToLower(path.Ext(name))]; ok {
			w.Header().Set("Content-Type", ct)
		} else if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	})

	api := func(path string, h func(w http.ResponseWriter, r *http.Request)) {
		mux.HandleFunc("/api/"+path, func(w http.ResponseWriter, r *http.Request) {
			if !allowedHosts[r.Host] {
				http.Error(w, "forbidden", 403)
				return
			}
			t := r.Header.Get("X-Token")
			if t == "" {
				t = r.URL.Query().Get("t")
			}
			if t != token {
				http.Error(w, "forbidden", 403)
				return
			}
			presence.heard(clientID(r))
			defer func() {
				if p := recover(); p != nil {
					logf("panic in %s: %v", path, p)
					writeErr(w, fmt.Errorf("something went wrong inside the launcher (%v). Press Copy support report in Settings and send it to @voidcallsreaper on Discord", p))
				}
			}()
			h(w, r)
		})
	}

	api("hello", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "version": appVersion, "build": buildID, "busy": busy.What()})
	})
	// a newer copy of the launcher has started: this one makes way for it
	api("quit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", 405)
			return
		}
		// held until the program has ended, so no switch or add starts meanwhile
		if !busy.Try("closing") {
			writeJSON(w, 200, map[string]any{"ok": false, "error": "busy: " + busy.What()})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
		go func() {
			time.Sleep(200 * time.Millisecond)
			logf("a newer launcher started; making way for it")
			os.Exit(0)
		}()
	})
	api("ping", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"ok": true}) })
	api("bye", func(w http.ResponseWriter, r *http.Request) {
		presence.bye(clientID(r))
		writeJSON(w, 200, map[string]any{"ok": true})
	})

	api("state", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, buildState()) })
	api("status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, buildStatus()) })

	// mutating calls: one at a time
	act := func(path, what string, h func(g Game, r *http.Request) (any, error)) {
		api(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "POST only", 405)
				return
			}
			// choosing the game folder doesn't need the game: it must not wait
			// while the launcher is still looking for it
			var g Game
			ok := false
			if path != "game" && path != "pick-game" {
				if g, ok = currentGame(); !ok {
					writeErr(w, errNoGame())
					return
				}
			}
			if ok && needsStorage[path] && g.storageMissing() {
				writeErr(w, fmt.Errorf("the mod storage folder (%s) can't be found. Connect the drive it is on, or choose it again in Settings", g.Storage()))
				return
			}
			if !busy.Try(what) {
				writeErr(w, errors.New("the launcher is still busy: "+busy.What()))
				return
			}
			v, err := func() (any, error) {
				defer busy.Done()
				return h(g, r)
			}()
			if err != nil {
				logf("%s failed: %v", path, err)
				writeErr(w, err)
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "result": v, "state": buildState()})
		})
	}

	act("switch", "switching", func(g Game, r *http.Request) (any, error) {
		var req struct{ To string }
		json.NewDecoder(r.Body).Decode(&req)
		if !gameRunning() {
			g.sweepUpdates() // an old copy left by an update cut short must not move along
		}
		return g.switchTo(req.To)
	})
	act("play", "starting the game", func(g Game, r *http.Request) (any, error) {
		var req struct{ Force bool }
		json.NewDecoder(r.Body).Decode(&req)
		if !gameRunning() {
			g.sweepUpdates() // an old copy left by an update cut short would load as well
		}
		return g.playWith(req.Force)
	})
	act("steam-launch-options", "changing Steam's launch options", func(_ Game, r *http.Request) (any, error) {
		if platform != "linux" {
			return nil, errors.New("this is only needed on Linux")
		}
		return setSteamLaunchOptions()
	})
	act("order", "saving the load order", func(g Game, r *http.Request) (any, error) {
		var req struct{ Mods []orderEntry }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return nil, err
		}
		ld := g.loaderDir()
		if !isDir(ld) {
			return nil, errors.New("the Mod Loader is not installed, so there is no load order to save")
		}
		return nil, writeLoadOrder(ld, keepSkinListLast(req.Mods))
	})
	act("script", "switching a script mod", func(g Game, r *http.Request) (any, error) {
		var req struct {
			Name    string
			Enabled bool
		}
		json.NewDecoder(r.Body).Decode(&req)
		return nil, setScriptMod(g.ue4ssRoot(), req.Name, req.Enabled)
	})
	act("setup", "setting up modding", func(g Game, r *http.Request) (any, error) { return g.setup() })
	act("add", "adding mods", func(g Game, r *http.Request) (any, error) {
		var req struct {
			Paths  []string
			Upload string
			Pick   []string
		}
		json.NewDecoder(r.Body).Decode(&req)
		return addFrom(g, req.Paths, req.Upload, req.Pick)
	})
	act("upload", "receiving files", func(g Game, r *http.Request) (any, error) { return receiveUpload(r) })
	// Choosing files changes nothing, so the file window doesn't hold the busy
	// lock: a mod dropped onto the launcher while it is open is added as usual.
	// One file window at a time, though.
	var pickMu sync.Mutex
	api("pick-mods", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", 405)
			return
		}
		if _, ok := currentGame(); !ok {
			writeErr(w, errNoGame())
			return
		}
		if !pickMu.TryLock() {
			writeErr(w, errors.New("a window to choose a mod is open already. Choose the mod there and press Open, or press Cancel"))
			return
		}
		v, err := func() (any, error) {
			defer pickMu.Unlock()
			return pickMods(r)
		}()
		if err != nil {
			logf("pick-mods failed: %v", err)
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "result": v, "state": buildState()})
	})
	act("remove", "removing a mod", func(g Game, r *http.Request) (any, error) {
		var req struct{ Kind, Key string }
		json.NewDecoder(r.Body).Decode(&req)
		if gameRunning() {
			return nil, errGameRunning
		}
		if req.Kind == "script" {
			for _, m := range listScriptMods(g.ue4ssRoot()) {
				if m.Name == req.Key {
					return g.removeToStorage("script", m.Name, m.Path, false)
				}
			}
			return nil, errors.New("that mod is no longer there")
		}
		mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
		for _, m := range mods {
			if m.Key == req.Key {
				dir, err := g.removeToStorage(map[string]string{"loader": "pak", "tilde": "tilde"}[m.Where], m.Key, m.Path, m.IsFile)
				if err == nil {
					g.tidyLoadOrder()
				}
				return dir, err
			}
		}
		return nil, errors.New("that mod is no longer there")
	})
	act("restore", "restoring a mod", func(g Game, r *http.Request) (any, error) {
		var req struct{ ID string }
		json.NewDecoder(r.Body).Decode(&req)
		return nil, g.restoreRemoved(req.ID)
	})
	act("delete-removed", "deleting a removed mod", func(g Game, r *http.Request) (any, error) {
		var req struct{ ID string }
		json.NewDecoder(r.Body).Decode(&req)
		return nil, g.deleteRemoved(req.ID)
	})
	act("game", "changing the game folder", func(_ Game, r *http.Request) (any, error) {
		var req struct{ Path string }
		json.NewDecoder(r.Body).Decode(&req)
		p, err := normalizeGameDir(req.Path)
		if err != nil {
			return nil, err
		}
		logf("game folder set to %s", p)
		return p, updateSettings(func(s *Settings) {
			keepStorageAfterMove(s, s.GameDir, p)
			s.GameDir = p
		})
	})
	act("pick-game", "waiting for you to choose the game folder", func(_ Game, r *http.Request) (any, error) {
		start := getSettings().GameDir
		if start == "" {
			if sd := steamDir(); sd != "" {
				start = join(sd, "steamapps", "common")
			}
		}
		p, err := pickFolder("Choose the ACE COMBAT 8 folder", start, false)
		if err != nil || p == "" {
			return "", err
		}
		root, err := normalizeGameDir(p)
		if err != nil {
			return nil, err
		}
		logf("game folder set to %s", root)
		return root, updateSettings(func(s *Settings) {
			keepStorageAfterMove(s, s.GameDir, root)
			s.GameDir = root
		})
	})
	act("storage", "moving the storage folder", func(g Game, r *http.Request) (any, error) {
		var req struct {
			Path    string
			Default bool
		}
		json.NewDecoder(r.Body).Decode(&req)
		if req.Default {
			return g.moveStorage("")
		}
		if strings.TrimSpace(req.Path) == "" {
			return nil, errors.New("no folder was chosen")
		}
		return g.moveStorage(g.storageTarget(expandHome(strings.Trim(strings.TrimSpace(req.Path), `"'`))))
	})
	act("pick-storage", "waiting for you to choose the storage folder", func(g Game, r *http.Request) (any, error) {
		p, err := pickFolder("Choose where to keep switched-off and removed mods", filepath.Dir(g.Storage()), true)
		if err != nil || p == "" {
			return StorageResult{}, err
		}
		return g.moveStorage(g.storageTarget(p))
	})
	// Extra launch options and "welcome shown" change no mod files, so they are
	// saved even while a switch or an add is running.
	api("settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", 405)
			return
		}
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		if err := updateSettings(func(s *Settings) {
			if v, ok := req["extraArgs"].(string); ok {
				s.ExtraArgs = strings.TrimSpace(v)
			}
			if v, ok := req["welcomeDone"].(bool); ok {
				s.WelcomeDone = v
			}
		}); err != nil {
			logf("settings failed: %v", err)
			writeErr(w, fmt.Errorf("the launcher could not save its settings (%v)", err))
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "result": nil, "state": buildState()})
	})

	api("reveal", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Path, What string }
		json.NewDecoder(r.Body).Decode(&req)
		g, _ := currentGame()
		p := req.Path
		switch req.What {
		case "storage":
			if g.Root == "" {
				writeErr(w, errors.New("the launcher does not know where Ace Combat 8 is yet. Choose the game folder first"))
				return
			}
			if g.storageMissing() {
				// making a new empty folder there would hide that the real one is missing
				writeErr(w, fmt.Errorf("the mod storage folder (%s) can't be found. Connect the drive it is on, or choose it again in Settings", g.Storage()))
				return
			}
			writeStorageReadme(g) // the default folder is made the first time it is needed
			p = g.Storage()
		case "loader":
			p = g.loaderDir()
		case "tilde":
			p = g.tildeModsDir()
			if !isDir(p) && g.Root != "" && g.modeInfo().Mode != modeUnmodded {
				os.MkdirAll(p, 0o755)
			}
		case "scripts":
			p = join(g.ue4ssRoot(), "Mods")
		case "game":
			p = g.Root
		case "data":
			p = dataDir
		case "launcherlog":
			p = join(dataDir, "launcher.log")
		case "loaderlog":
			p = join(g.loaderDir(), "AC8IoStoreLoader.log")
		}
		if p == "" || !allowedReveal(g, p) {
			writeErr(w, errors.New("that folder is not one the launcher looks after"))
			return
		}
		if err := revealPath(p); err != nil {
			writeErr(w, errors.New("could not open "+p))
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	api("open", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ URL string }
		json.NewDecoder(r.Body).Decode(&req)
		ok := false
		for _, prefix := range []string{"steam://", "https://aka.ms/vs/17/release/vc_redist.x64.exe", "https://www.nexusmods.com/"} {
			if strings.HasPrefix(req.URL, prefix) {
				ok = true
			}
		}
		if !ok {
			writeErr(w, errors.New("that link is not allowed"))
			return
		}
		if err := openURL(req.URL, false); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	api("loader-log", func(w http.ResponseWriter, r *http.Request) {
		g, _ := currentGame()
		b, err := os.ReadFile(join(g.loaderDir(), "AC8IoStoreLoader.log"))
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": true, "text": ""})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "text": decodeText(b)})
	})
	api("ue4ss-log", func(w http.ResponseWriter, r *http.Request) {
		g, _ := currentGame()
		b, err := os.ReadFile(join(g.ue4ssRoot(), "UE4SS.log"))
		if err != nil {
			writeJSON(w, 200, map[string]any{"ok": true, "text": ""})
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "text": tail(decodeText(b), 400)})
	})
	api("report", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "text": supportReport()})
	})
	return mux
}

// needsStorage: actions that use the storage folder, refused while a chosen
// storage folder can't be found.
var needsStorage = map[string]bool{"switch": true, "setup": true, "add": true, "upload": true, "remove": true, "restore": true, "delete-removed": true}

// allowedReveal: Explorer is only opened on the game, its mod folders, the storage and the launcher's own data.
func allowedReveal(g Game, p string) bool {
	p = filepath.Clean(p)
	for _, base := range []string{g.Root, g.Storage(), dataDir} {
		if base == "" {
			continue
		}
		b := filepath.Clean(base)
		if strings.EqualFold(p, b) || strings.HasPrefix(strings.ToLower(p), strings.ToLower(b)+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ---- state -------------------------------------------------------------------

type GameState struct {
	Found             bool   `json:"found"`
	Root              string `json:"root"`
	Storage           string `json:"storage"`
	DefaultStorage    string `json:"defaultStorage"`
	StorageCustom     bool   `json:"storageCustom"`
	StorageOtherDrive bool   `json:"storageOtherDrive"`
	StorageMissing    bool   `json:"storageMissing"`
	LoaderDir         string `json:"loaderDir"`
	TildeDir          string `json:"tildeDir"`
	SteamDir          string `json:"steamDir"`
	// not found: the game folder chosen before, which can't be found now (a
	// drive not connected), and another copy found meanwhile, which the
	// player can choose instead (the "game" call with its path)
	Missing   string `json:"missing,omitempty"`
	Elsewhere string `json:"elsewhere,omitempty"`
}

type State struct {
	Version      string       `json:"version"`
	Contact      string       `json:"contact"`
	Settings     Settings     `json:"settings"`
	Game         GameState    `json:"game"`
	Mode         ModeInfo     `json:"mode"`
	Setup        SetupStatus  `json:"setup"`
	PakMods      []*PakMod    `json:"pakMods"`
	StaleOrder   []string     `json:"staleOrder"`
	ScriptMods   []*ScriptMod `json:"scriptMods"`
	Removed      []RemovedMod `json:"removed"`
	Run          *RunInfo     `json:"run"`
	ScriptRun    *ScriptRun   `json:"scriptRun,omitempty"`
	SteamOptions []string     `json:"steamOptions"`
	SteamOffline bool         `json:"steamOffline"`
	Status       Status       `json:"status"`
	DirectCmd    string       `json:"directCmd"`
	Platform     string       `json:"platform"`
	HasPicker    bool         `json:"hasPicker"`
	Linux        *LinuxLaunch `json:"linux,omitempty"`
	UnfinishedSw string       `json:"unfinishedSwitch"`
}

type Status struct {
	GameRunning  bool        `json:"gameRunning"`
	SteamRunning bool        `json:"steamRunning"`
	Busy         string      `json:"busy"`
	Fingerprint  string      `json:"fingerprint"`
	Launch       *LaunchInfo `json:"launch"`
	LogFresh     bool        `json:"logFresh"` // the loader wrote its log since the last Play
	// Linux: the game started while Modded, but not through the launcher's script
	SteamStartMissed bool `json:"steamStartMissed,omitempty"`
}

func buildStatus() Status {
	st := Status{GameRunning: gameRunning(), SteamRunning: steamRunning(), Busy: busy.What(), Launch: currentLaunch()}
	g, ok := currentGame()
	var fp strings.Builder
	if ok {
		for _, p := range []string{g.Root, g.Win64(), g.Paks(), g.loaderDir(), g.tildeModsDir(), join(g.ue4ssRoot(), "Mods"),
			g.Storage(), g.OffDir(), g.RemovedDir(), join(g.loaderDir(), "AC8IoStoreLoader.log"), join(g.loaderDir(), "loadorder.txt"), join(g.ue4ssRoot(), "Mods", "mods.txt")} {
			if s, err := os.Stat(p); err == nil {
				fmt.Fprintf(&fp, "%d.", s.ModTime().UnixNano())
			} else {
				fp.WriteString("-.")
			}
		}
		if st.Launch != nil {
			if s, err := os.Stat(join(g.loaderDir(), "AC8IoStoreLoader.log")); err == nil && s.ModTime().After(st.Launch.LogBefore) && s.ModTime().After(st.Launch.Started.Add(-2*time.Second)) {
				st.LogFresh = true
			}
		}
	}
	fp.WriteString(getSettings().GameDir)
	// the Mod Loader download put next to the launcher (or taken away), so the
	// window offers Install without a restart
	fp.WriteString("|" + bundleStamp())
	if platform == "linux" {
		if t := steamStartLogTime(); !t.IsZero() {
			fmt.Fprintf(&fp, ".%d", t.UnixNano())
		}
		fp.WriteString(steamConfigStamp())
		if L := st.Launch; L != nil && L.Mode == modeModded && L.SawGame && steamStartLogTime().Before(L.Started.Add(-5*time.Second)) {
			st.SteamStartMissed = true
		}
	}
	st.Fingerprint = fp.String()
	return st
}

func buildState() State {
	s := State{Version: appVersion, Contact: contact, Settings: getSettings(), Status: buildStatus(),
		DirectCmd: `AceCombat8.exe -eac-nop-loaded -nosplash`, Platform: platform, HasPicker: hasPicker()}
	if platform == "linux" {
		s.Linux = linuxLaunchState()
	}
	s.Game.SteamDir = steamDir()
	s.SteamOptions = steamLaunchOptions()
	for _, o := range s.SteamOptions {
		if isOfflineLaunchOptions(o) {
			s.SteamOffline = true
		}
	}
	g, ok := currentGame()
	if !ok {
		s.Game.Missing, s.Game.Elsewhere = gameMissing()
		s.Mode = ModeInfo{Mode: modeNone}
		s.PakMods, s.ScriptMods, s.Removed = []*PakMod{}, []*ScriptMod{}, []RemovedMod{}
		return s
	}
	s.Settings = getSettings()
	s.Game = GameState{Found: true, Root: g.Root, Storage: g.Storage(), DefaultStorage: g.DefaultStorage(), StorageCustom: g.customStorage(),
		StorageOtherDrive: !sameDrive(g.Storage(), g.Root), StorageMissing: g.storageMissing(),
		LoaderDir: g.loaderDir(), TildeDir: g.tildeModsDir(), SteamDir: s.Game.SteamDir}
	s.Mode = g.modeInfo()
	s.Setup = g.setupStatus()
	s.Run = readRunInfo(g.loaderDir())
	s.ScriptRun = readScriptRun(g.ue4ssRoot())
	s.PakMods, s.StaleOrder = scanPakMods(g.loaderDir(), g.tildeModsDir())
	if skinListNotLast(s.PakMods) && busy.Try("tidying the load order") {
		// Skin Studio's skin list has to load after the skins; put it back at the end.
		g.tidyLoadOrder()
		busy.Done()
		logf("moved Skin Studio's skin list to the end of the load order")
		s.PakMods, s.StaleOrder = scanPakMods(g.loaderDir(), g.tildeModsDir())
	}
	addModNotes(s.PakMods, s.Run, s.Mode.Mode)
	if s.PakMods == nil {
		s.PakMods = []*PakMod{}
	}
	s.ScriptMods = listScriptMods(g.ue4ssRoot())
	dates := loadModDates()
	for _, m := range s.PakMods {
		m.Added, m.Updated = datesFor(dates, pakDateKey(m.Key))
	}
	for _, m := range s.ScriptMods {
		m.Added, m.Updated = datesFor(dates, scriptDateKey(m.Name))
	}
	if s.ScriptMods == nil {
		s.ScriptMods = []*ScriptMod{}
	}
	s.Removed = g.listRemoved()
	if s.Removed == nil {
		s.Removed = []RemovedMod{}
	}
	s.UnfinishedSw = g.unfinishedSwitch()
	return s
}

// ---- adding: uploads and paths ----------------------------------------------------

func uploadsDir() string { return join(dataDir, "uploads") }

// maxUpload: a drop bigger than this is not a mod (the game's own folder is
// tens of GB). Add a mod… and Add unzipped folder… read a very big mod where it is.
var maxUpload int64 = 16 << 30

var errUploadTooBig = errors.New("upload too big")

func uploadTooBig(n int64) error {
	size := "more than " + gb(maxUpload)
	if n > 0 {
		size = gb(n)
	}
	return fmt.Errorf("what you dropped is %s, which is far too big for a mod. Drop only the mod you downloaded, not the game's folder. For a very big mod, use Add a mod… or Add unzipped folder… on the Mods page instead", size)
}

func gb(n int64) string { return fmt.Sprintf("%.1f GB", float64(n)/(1<<30)) }

// capReader stops a drop that turns out bigger than max.
type capReader struct {
	r      io.Reader
	n, max int64
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if c.n += int64(n); c.n > c.max {
		return n, errUploadTooBig
	}
	return n, err
}

// errGameFiles: the game's own files are not a mod. Added, they would be
// loaded a second time and break the game (and take tens of GB).
func errGameFiles(what, file string) error {
	return fmt.Errorf("%s holds the game's own files (such as %s), not a mod. Add only the mod you downloaded", what, file)
}

// uploadSegments cleans the path of a dropped file. Names Windows can't store
// (with : * ? " < > |, possible on Linux) get "_" in their place, so the files
// of one mod stay together. Junk from other systems (.DS_Store, Thumbs.db) is
// left out (false).
func uploadSegments(rel string) ([]string, bool) {
	parts := strings.Split(strings.ReplaceAll(rel, `\`, "/"), "/")
	for i, s := range parts {
		if s == "." || s == ".." || s == "" {
			continue // cleanSegments refuses ".."
		}
		s = strings.Map(func(r rune) rune {
			if r < 32 || strings.ContainsRune(`:*?"<>|`, r) {
				return '_'
			}
			return r
		}, s)
		if t := strings.TrimRight(s, ". "); t != "" && !windowsSafeName(t) {
			s = "_" + s // CON, NUL.txt …
		}
		parts[i] = s
	}
	return cleanSegments(strings.Join(parts, "/"))
}

// receiveUpload saves files dropped onto the window into uploads\<id>, as they
// arrive: nothing is kept in memory or in a temporary copy first, so a drop
// needs its own size on the disk once, and any number of files is fine. What
// was saved of a drop that fails is deleted again.
func receiveUpload(r *http.Request) (_ any, err error) {
	if r.ContentLength > maxUpload {
		return nil, uploadTooBig(r.ContentLength)
	}
	body := &capReader{r: r.Body, max: maxUpload}
	r.Body = io.NopCloser(body)
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, errors.New("the files could not be received: " + err.Error())
	}
	id := newToken()[:12]
	dir := join(uploadsDir(), id)
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
			if body.n > body.max {
				err = uploadTooBig(r.ContentLength)
			}
		}
	}()
	var paths []string // the window sends each file's path before the file
	files, kept := 0, 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("the files could not be received: " + err.Error())
		}
		switch part.FormName() {
		case "path":
			b, _ := io.ReadAll(io.LimitReader(part, 4096))
			paths = append(paths, string(b))
		case "file":
			rel := part.FileName()
			if files < len(paths) && paths[files] != "" {
				rel = paths[files]
			}
			files++
			segs, ok := uploadSegments(rel)
			if !ok {
				break
			}
			if b := strings.ToLower(segs[len(segs)-1]); b == "global.utoc" || b == "global.ucas" {
				what := "What you dropped"
				if len(segs) > 1 {
					what = segs[0]
				}
				part.Close()
				return nil, errGameFiles(what, segs[len(segs)-1]) // stop before tens of GB arrive
			}
			dst := join(dir, filepath.Join(segs...))
			err := os.MkdirAll(filepath.Dir(dst), 0o755)
			var out *os.File
			if err == nil {
				out, err = os.Create(dst)
			}
			if err == nil {
				_, err = io.Copy(out, part)
				if cerr := out.Close(); err == nil {
					err = cerr
				}
			}
			if err != nil {
				part.Close()
				return nil, fmt.Errorf("could not save %s: %v", strings.Join(segs, "/"), err)
			}
			kept++
		}
		part.Close()
	}
	if kept == 0 {
		return nil, errors.New("nothing in what you dropped can be used. Drop the mod's folder or its .zip file")
	}
	return id, nil
}

// bundleStamp changes when a Mod Loader download appears next to the
// launcher, goes, or is replaced by another version. bundleDir keeps its
// answer for a few seconds, so this is cheap.
func bundleStamp() string {
	bd := bundleDir()
	if bd == "" {
		return ""
	}
	if st, err := os.Stat(join(bd, "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "dlls", "main.dll")); err == nil {
		return fmt.Sprintf("%s.%d.%d", bd, st.Size(), st.ModTime().UnixNano())
	}
	return bd
}

// gameOwnFile names a file of the game itself among files, "" when there is
// none: global.utoc or global.ucas, which only the game has, or a container
// with the name and size of one in the game's Paks folder (pakchunk0-Windows.ucas …).
// Some mods are called pakchunkNN-Windows too, so the name alone is not enough.
func gameOwnFile(g Game, files []incoming) string {
	sizes := map[string]int64{}
	if ents, err := os.ReadDir(g.Paks()); err == nil {
		for _, e := range ents {
			if info, err := e.Info(); err == nil && !e.IsDir() && isContainerFile(e.Name()) {
				sizes[strings.ToLower(e.Name())] = info.Size()
			}
		}
	}
	for _, f := range files {
		base := path.Base(strings.ReplaceAll(f.rel, `\`, "/"))
		low := strings.ToLower(base)
		if low == "global.utoc" || low == "global.ucas" {
			return base
		}
		if size, ok := sizes[low]; ok && size == f.size {
			return base
		}
	}
	return ""
}

func cleanOldUploads() {
	ents, _ := os.ReadDir(uploadsDir())
	for _, e := range ents {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 6*time.Hour {
			os.RemoveAll(join(uploadsDir(), e.Name()))
		}
	}
}

// addFrom adds mods from paths on this PC (a .zip, a folder, or loose files) or from an upload.
func addFrom(g Game, paths []string, upload string, pick []string) (AddResult, error) {
	cleanOldUploads()
	if upload != "" {
		if strings.ContainsAny(upload, `/\.`) {
			return AddResult{}, errors.New("unknown upload")
		}
		dir := join(uploadsDir(), upload)
		ents, err := os.ReadDir(dir)
		if err != nil {
			return AddResult{}, errors.New("the dropped files are gone; drop them again")
		}
		paths = nil
		for _, e := range ents {
			paths = append(paths, join(dir, e.Name()))
		}
		res, err := addFromPaths(g, paths, pick)
		if err != nil || len(res.Choices) == 0 {
			os.RemoveAll(dir)
		}
		return res, err
	}
	return addFromPaths(g, paths, pick)
}

func addFromPaths(g Game, paths []string, pick []string) (AddResult, error) {
	if len(paths) == 0 {
		return AddResult{}, errors.New("nothing to add")
	}
	// a typed path (no file chooser on this computer) may have quotes or "~/"
	paths = append([]string(nil), paths...)
	for i, p := range paths {
		paths[i] = expandHome(strings.Trim(strings.TrimSpace(p), `"'`))
	}
	for _, p := range paths {
		// A mod is added from where it was downloaded. Adding something that is
		// already in the game (or in the launcher's storage) would move it onto itself.
		for _, inside := range []string{g.Root, g.Storage()} {
			if isInside(p, inside) || isInside(inside, p) {
				return AddResult{}, errors.New(filepath.Base(p) + " is part of the game or of the launcher's storage folder, so it can't be added from there. Add mods from where you downloaded them, such as your Downloads folder. Mods already in the game are on the Mods page")
			}
		}
	}
	var all AddResult
	merge := func(r AddResult) {
		all.Added = append(all.Added, r.Added...)
		all.Updated = append(all.Updated, r.Updated...)
		all.Notes = append(all.Notes, r.Notes...)
		all.Warnings = append(all.Warnings, r.Warnings...)
		all.Choices = append(all.Choices, r.Choices...)
		all.SkinStudio = all.SkinStudio || r.SkinStudio
	}
	// Each download is read first and added after: so the game's own files are
	// refused, and several downloads' versions asked about, before anything is added.
	type download struct {
		source, what string
		files        []incoming
	}
	var downloads []download
	var closers []func()
	defer func() {
		for _, c := range closers {
			c()
		}
	}()
	queue := func(source, what string, files []incoming) {
		downloads = append(downloads, download{source, what, files})
	}
	var loose []incoming
	looseName, pakName := "", ""
	sort.Strings(paths)
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return all, errors.New(filepath.Base(p) + " could not be read")
		}
		ext := strings.ToLower(filepath.Ext(p))
		switch {
		case st.IsDir():
			files, err := readFolder(p)
			if err != nil {
				return all, err
			}
			queue(filepath.Base(p), filepath.Base(p), files)
		case ext == ".zip":
			files, closer, err := readZip(p)
			if err != nil {
				return all, err
			}
			closers = append(closers, closer)
			queue(strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)), filepath.Base(p), files)
		case ext == ".7z" || ext == ".rar":
			return all, errors.New(filepath.Base(p) + " is a " + ext + " archive. Unpack it first (for example with 7-Zip), then add the folder that comes out")
		default:
			p2 := p
			loose = append(loose, incoming{rel: filepath.Base(p), open: func() (io.ReadCloser, error) { return os.Open(p2) }, size: st.Size(), mtime: st.ModTime()})
			if ext == ".utoc" && looseName == "" {
				looseName = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			}
			if isPakName(p) && pakName == "" {
				pakName = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			}
		}
	}
	if len(loose) > 0 {
		// loose files are named after their first .utoc, or *_P.pak (Mod Loader
		// 1.3): one name for all of them would make every new mod look like an
		// update of the last one
		if looseName == "" {
			looseName = pakName
		}
		if looseName == "" {
			looseName = "New mod"
		}
		// loose .utoc files picked on their own: bring their .ucas and .pak along
		have := map[string]bool{}
		for _, f := range loose {
			have[strings.ToLower(f.rel)] = true
		}
		for _, p := range paths {
			if strings.EqualFold(filepath.Ext(p), ".utoc") {
				base := strings.TrimSuffix(p, filepath.Ext(p))
				for _, ext := range []string{".ucas", ".pak"} {
					if st, err := os.Stat(base + ext); err == nil && !have[strings.ToLower(filepath.Base(base+ext))] {
						q := base + ext
						loose = append(loose, incoming{rel: filepath.Base(q), open: func() (io.ReadCloser, error) { return os.Open(q) }, size: st.Size(), mtime: st.ModTime()})
					}
				}
			}
		}
		queue(looseName, "What you chose", loose)
	}
	// the game's own Paks (or whole) folder is not a mod
	for _, d := range downloads {
		if f := gameOwnFile(g, d.files); f != "" {
			return AddResult{}, errGameFiles(d.what, f)
		}
	}
	if pick == nil && len(downloads) > 1 {
		var ch []AddChoice
		for _, d := range downloads {
			ch = append(ch, g.versionChoices(d.source, d.files)...)
		}
		if len(ch) > 0 {
			return AddResult{Choices: ch}, nil
		}
	}
	for _, d := range downloads {
		r, err := g.addMods(d.source, d.files, pick)
		if err != nil {
			return all, err
		}
		merge(r)
	}
	return all, nil
}

// ---- support report ----------------------------------------------------------------

func supportReport() string {
	var b strings.Builder
	st := buildState()
	home, _ := os.UserHomeDir()
	user := filepath.Base(home)
	fmt.Fprintf(&b, "%s %s on %s/%s\n", appName, appVersion, runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "Game folder: %s\n", st.Game.Root)
	fmt.Fprintf(&b, "Mode: %s  (game running: %v, Steam running: %v)\n", st.Mode.Mode, st.Status.GameRunning, st.Status.SteamRunning)
	for _, it := range st.Mode.Items {
		fmt.Fprintf(&b, "  %-10s in game: %-5v switched off: %v\n", it.Key, it.InGame, it.Parked)
	}
	fmt.Fprintf(&b, "UE4SS: %v  Mod Loader: %q (on: %v)  bundled: %q  VC++ runtime: %v\n",
		st.Setup.UE4SS, st.Setup.Loader, st.Setup.LoaderOn, st.Setup.Bundled, st.Setup.VCRuntime)
	fmt.Fprintf(&b, "Extra launch options: %q\n", st.Settings.ExtraArgs)
	fmt.Fprintf(&b, "Steam launch options set: %v (offline line: %v)\n", len(st.SteamOptions) > 0, st.SteamOffline)
	if L := st.Linux; L != nil {
		fmt.Fprintf(&b, "Linux: Steam dir %s\n  launch script %s\n  in Steam's launch options: %v (other place: %v)\n  launch options now: %q\n",
			st.Game.SteamDir, L.Script, L.Set, L.Stale, L.Current)
		if lb, err := os.ReadFile(join(filepath.Dir(L.Script), "steam-launch.log")); err == nil {
			b.WriteString("\n---- steam-launch.log (last lines) ----\n")
			b.WriteString(tail(string(lb), 15))
		}
	}
	fmt.Fprintf(&b, "\nPak mods (load order):\n")
	for _, m := range st.PakMods {
		fmt.Fprintf(&b, "  [%s] %s  (%s, %d file(s), %d to load)\n", map[bool]string{true: "on ", false: "off"}[m.Enabled], m.Key, m.Where, len(m.Containers), m.Loads)
		for _, n := range m.Notes {
			fmt.Fprintf(&b, "        %s: %s\n", n.Kind, n.Text)
		}
	}
	fmt.Fprintf(&b, "\nScript mods:\n")
	for _, m := range st.ScriptMods {
		fmt.Fprintf(&b, "  [%s] %s (%s)\n", map[bool]string{true: "on ", false: "off"}[m.Enabled], m.Name, m.Kind)
	}
	if st.Run != nil {
		fmt.Fprintf(&b, "\nLast game start (%s): %s %s\n", st.Run.When.Format("2006-01-02 15:04"), st.Run.State, st.Run.Summary)
		for _, p := range st.Run.Problems {
			fmt.Fprintf(&b, "  Mod Loader: %s\n", p)
		}
	}
	if st.ScriptRun != nil {
		for _, p := range st.ScriptRun.Problems {
			fmt.Fprintf(&b, "  UE4SS: %s\n", p)
		}
	}
	if g, ok := currentGame(); ok {
		if lb, err := os.ReadFile(join(g.loaderDir(), "AC8IoStoreLoader.log")); err == nil {
			b.WriteString("\n---- AC8IoStoreLoader.log ----\n")
			b.WriteString(tail(decodeText(lb), 120))
		}
		if lb, err := os.ReadFile(join(g.ue4ssRoot(), "UE4SS.log")); err == nil {
			b.WriteString("\n---- UE4SS.log (last lines) ----\n")
			b.WriteString(tail(decodeText(lb), 60))
		}
	}
	if lb, err := os.ReadFile(join(dataDir, "launcher.log")); err == nil {
		b.WriteString("\n---- launcher.log (last lines) ----\n")
		b.WriteString(tail(string(lb), 60))
	}
	out := b.String()
	if user != "" && user != "." && len(user) > 1 {
		// any spelling: C:\Users\Name\, c:/users/name/, /home/name/
		re := regexp.MustCompile(`(?i)([\\/](?:Users|home)[\\/])` + regexp.QuoteMeta(user) + `([\\/])`)
		out = re.ReplaceAllString(out, "${1}<you>${2}")
	}
	return out
}

// isInside reports whether p is dir or inside it (case-insensitive, as on Windows).
func isInside(p, dir string) bool {
	if p == "" || dir == "" {
		return false
	}
	ap, err1 := filepath.Abs(p)
	ad, err2 := filepath.Abs(dir)
	if err1 != nil || err2 != nil {
		return false
	}
	ap, ad = strings.ToLower(filepath.Clean(ap)), strings.ToLower(filepath.Clean(ad))
	return ap == ad || strings.HasPrefix(ap, strings.TrimSuffix(ad, string(filepath.Separator))+string(filepath.Separator))
}

func tail(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > n {
		lines = append([]string{"…"}, lines[len(lines)-n:]...)
	}
	return strings.Join(lines, "\n") + "\n"
}

func skinListNotLast(mods []*PakMod) bool {
	seen := false
	for _, m := range mods {
		if m.SkinList {
			seen = true
		} else if seen {
			return true
		}
	}
	return false
}

// pickMods opens the file window to choose a mod's files (or its folder).
func pickMods(r *http.Request) (any, error) {
	var req struct{ Folder bool }
	json.NewDecoder(r.Body).Decode(&req)
	if req.Folder {
		p, err := pickFolder("Choose the folder of the mod you want to add", "", false)
		if p == "" || err != nil {
			return []string{}, err
		}
		return []string{p}, nil
	}
	ps, err := pickFiles("Choose a mod: its .zip, or its .utoc, .ucas and .pak files")
	if ps == nil {
		ps = []string{}
	}
	return ps, err
}
