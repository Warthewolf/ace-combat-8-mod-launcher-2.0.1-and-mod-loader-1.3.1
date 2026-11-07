package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Settings struct {
	GameDir     string `json:"gameDir"`
	ExtraArgs   string `json:"extraArgs"`
	WelcomeDone bool   `json:"welcomeDone"`
	// StorageDir is a mod storage folder the player chose, for the game in
	// StorageGame ("" = the default, next to the game).
	StorageDir  string `json:"storageDir,omitempty"`
	StorageGame string `json:"storageGame,omitempty"`
}

var (
	settingsMu sync.Mutex
	settings   Settings
)

func settingsPath() string { return filepath.Join(dataDir, "settings.json") }

func loadSettings() {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	settings = Settings{}
	if b, err := os.ReadFile(settingsPath()); err == nil {
		json.Unmarshal(b, &settings)
	}
	// A folder typed without its full path (possible on Linux without a folder
	// chooser) was saved as it was typed, so it meant something else each time
	// the launcher started from another folder, and a chosen storage folder no
	// longer matched its game. Full paths from now on.
	for _, p := range []*string{&settings.GameDir, &settings.StorageDir, &settings.StorageGame} {
		if *p != "" && !filepath.IsAbs(*p) {
			if abs, err := filepath.Abs(*p); err == nil {
				*p = abs
			}
		}
	}
}

func getSettings() Settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return settings
}

// updateSettings changes the settings and saves them. The launcher only goes
// on with the new settings once they are saved: when they can't be, it keeps
// the old ones, so what it does always matches settings.json.
func updateSettings(f func(*Settings)) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	next := settings
	f(&next)
	b, _ := json.MarshalIndent(next, "", "  ")
	tmp := settingsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, settingsPath()); err != nil {
		os.Remove(tmp)
		return err
	}
	settings = next
	return nil
}
