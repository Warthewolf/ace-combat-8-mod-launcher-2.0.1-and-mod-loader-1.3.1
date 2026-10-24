package main

// Starting the game.
//
// Unmodded: Steam's own Play button (steam://rungameid/…), so the game starts
// exactly as Steam installed it, with anti-cheat.
//
// Modded: the game's own AceCombat8.exe is started directly, without the
// anti-cheat bootstrapper, with -eac-nop-loaded -nosplash and the Steam app id in
// the environment (SteamAppId / SteamGameId), the same as ac8noeac.bat. Nothing in
// the game folder or in Steam has to change for it, and Steam's Play button stays
// normal for online play.

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

var directArgs = []string{"-eac-nop-loaded", "-nosplash"}
var directEnv = []string{"SteamAppId=" + steamAppID, "SteamGameId=" + steamAppID}

// how long Play waits for Steam to finish signing in
var steamSignInWait = 90 * time.Second

var gameExeNames = []string{"AceCombat8.exe", "start_protected_game.exe"}

func gameRunning() bool { return processRunning(gameExeNames...) }

// what the community put in Steam's launch options for modded play
func isOfflineLaunchOptions(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "eos_use_anticheatclientnull") || strings.Contains(l, "anticheat_settings")
}

type LaunchInfo struct {
	Started   time.Time `json:"started"`
	Mode      string    `json:"mode"`
	How       string    `json:"how"`
	SawGame   bool      `json:"sawGame"`
	GameEnded bool      `json:"gameEnded"`
	Note      string    `json:"note,omitempty"` // something the player should know about this start
	LogBefore time.Time `json:"-"`
}

var (
	launchMu   sync.Mutex
	lastLaunch *LaunchInfo
)

// play starts the game for the current mode.
func (g Game) play() (*LaunchInfo, error) { return g.playWith(false) }

// playWith: force starts a Modded game on Linux even when Steam's launch
// options don't seem to have the launcher's script (Steam may not have saved
// them yet).
func (g Game) playWith(force bool) (*LaunchInfo, error) {
	if gameRunning() {
		return nil, errors.New("Ace Combat 8 is already running")
	}
	mi := g.modeInfo()
	if mi.Mode == modeMixed {
		return nil, errors.New("the switch is half done. Press Modded or Unmodded first so everything is in one place")
	}
	li := &LaunchInfo{Started: time.Now(), Mode: mi.Mode}
	if st, err := os.Stat(join(g.loaderDir(), "AC8IoStoreLoader.log")); err == nil {
		li.LogBefore = st.ModTime()
	}
	s := getSettings()
	if platform == "linux" {
		if err := g.playLinux(li, mi.Mode, force); err != nil {
			return nil, err
		}
	} else if mi.Mode != modeModded {
		li.How = "steam"
		if err := openURL("steam://rungameid/"+steamAppID, false); err != nil {
			return nil, errors.New("Steam could not be asked to start the game: " + err.Error())
		}
	} else {
		li.How = "direct"
		note, err := g.startDirect(s.ExtraArgs)
		if err != nil {
			return nil, err
		}
		li.Note = note
	}
	launchMu.Lock()
	lastLaunch = li
	launchMu.Unlock()
	logf("play: mode=%s how=%s", li.Mode, li.How)
	go watchLaunch(li)
	return li, nil
}

var errLaunchOptionMissing = errors.New("Steam's launch options for ACE COMBAT 8 don't have the launcher's line yet, so the game would start with anti-cheat on and without mods. Add it in Settings, Starting the game")

// playLinux: Steam starts the game in both modes; for Modded, the launcher's
// script in Steam's launch options turns anti-cheat off and loads UE4SS.
func (g Game) playLinux(li *LaunchInfo, mode string, force bool) error {
	li.How = "steam"
	if mode == modeModded {
		if err := writeWrapper(); err != nil {
			return fmt.Errorf("the Steam launch script could not be written (%v)", err)
		}
		if !force && !linuxLaunchState().Set {
			return errLaunchOptionMissing
		}
	}
	if err := openURL("steam://rungameid/"+steamAppID, false); err != nil {
		return errors.New("Steam could not be asked to start the game: " + err.Error())
	}
	return nil
}

// ensureSteam makes sure Steam is running and signed in: the game signs in
// through it. Steam is started in the background (-silent), so its window does
// not cover the game.
func ensureSteam() error {
	if !steamRunning() {
		logf("starting Steam first")
		if exe := join(steamDir(), "steam.exe"); steamDir() != "" && isFile(exe) {
			if err := startDetached(exe, steamDir(), []string{"-silent"}, "", nil); err != nil {
				openURL("steam://open/main", false)
			}
		} else {
			openURL("steam://open/main", false)
		}
		for i := 0; i < 60 && !steamRunning(); i++ {
			time.Sleep(500 * time.Millisecond)
		}
		if !steamRunning() {
			return errors.New("Steam did not start. Start Steam yourself, then press Play again")
		}
		time.Sleep(3 * time.Second)
	}
	// Steam notes the signed-in account in the registry; wait for it (signing in
	// and updates can take a while after Steam starts).
	for deadline := time.Now().Add(steamSignInWait); ; time.Sleep(500 * time.Millisecond) {
		if user, known := steamActiveUser(); !known || user != 0 {
			return nil
		}
		if time.Now().After(deadline) {
			break
		}
	}
	return errors.New("Steam is open but not signed in. Sign in to Steam, then press Play again")
}

func (g Game) startDirect(extra string) (note string, err error) {
	if !isFile(g.GameExe()) {
		return "", errors.New("AceCombat8.exe was not found in Game\\Binaries\\Win64. Check the game folder in Settings")
	}
	if err := ensureSteam(); err != nil {
		return "", err
	}
	extra = strings.TrimSpace(extra)
	err = startDetached(g.GameExe(), g.Win64(), directArgs, extra, directEnv)
	if errors.Is(err, errElevationRequired) {
		return g.startAsAdmin(extra)
	}
	if err != nil {
		return "", errors.New("the game could not be started: " + err.Error())
	}
	return "", nil
}

// Windows error codes: the program must run as administrator; the player said
// no to Windows' permission prompt.
var (
	errElevationRequired = syscall.Errno(740)
	errDeclined          = syscall.Errno(1223)
)

// startAsAdmin starts a game that is set to run as administrator (usually the
// "Run this program as an administrator" tick in AceCombat8.exe's
// Compatibility settings). Only Windows' own "open" can show the permission
// prompt for that, and settings made for the game (SteamAppId) don't reach a
// program started that way, so the Steam app id goes in steam_appid.txt, the
// file Steam games read when they are not started by Steam.
func (g Game) startAsAdmin(extra string) (string, error) {
	idFile := join(g.Win64(), "steam_appid.txt")
	if b, err := os.ReadFile(idFile); err != nil || strings.TrimSpace(string(b)) != steamAppID {
		if err := os.WriteFile(idFile, []byte(steamAppID), 0o644); err != nil {
			logf("could not write %s: %v", idFile, err)
		} else {
			logf("wrote %s for a game that runs as administrator", idFile)
		}
	}
	logf("AceCombat8.exe needs administrator rights; asking Windows to start it")
	if err := startElevated(g.GameExe(), g.Win64(), directArgs, extra); err != nil {
		if errors.Is(err, errDeclined) {
			return "", errors.New("Windows asked for permission to start the game as administrator, and it was not given. " + adminTip(g.GameExe()))
		}
		return "", errors.New("the game could not be started as administrator: " + err.Error())
	}
	return "AceCombat8.exe is set to run as administrator, so Windows asks for permission each time. " + adminTip(g.GameExe()), nil
}

func adminTip(exe string) string {
	if runAsAdminSet(exe) {
		return "To stop that, right-click AceCombat8.exe, choose Properties, then Compatibility, and untick \"Run this program as an administrator\" (also under \"Change settings for all users\")."
	}
	return "Something on this PC makes AceCombat8.exe ask for administrator rights."
}

// splitArgs splits extra launch options, keeping "quoted parts" together.
func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	for _, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
		case (r == ' ' || r == '\t') && !inQ:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// watchLaunch notes when the game shows up and when it ends.
func watchLaunch(li *LaunchInfo) {
	for i := 0; i < 90*2; i++ { // up to 90 s for the game to appear
		time.Sleep(500 * time.Millisecond)
		if gameRunning() {
			launchMu.Lock()
			li.SawGame = true
			launchMu.Unlock()
			break
		}
	}
	if !li.SawGame {
		return
	}
	for {
		time.Sleep(2 * time.Second)
		if !gameRunning() {
			launchMu.Lock()
			li.GameEnded = true
			launchMu.Unlock()
			logf("game closed")
			return
		}
	}
}

func currentLaunch() *LaunchInfo {
	launchMu.Lock()
	defer launchMu.Unlock()
	if lastLaunch == nil {
		return nil
	}
	c := *lastLaunch
	return &c
}
