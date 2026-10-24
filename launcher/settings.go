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
}

func getSettings() Settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return settings
}

func updateSettings(f func(*Settings)) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	f(&settings)
	b, _ := json.MarshalIndent(settings, "", "  ")
	tmp := settingsPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, settingsPath())
}
