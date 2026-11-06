package main

// When each mod was added to the game, or last updated, through the launcher.
// Kept in mod-dates.json in the launcher's data folder, by the mod's name, so it
// stays the same while the game is switched to Unmodded.

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

type modDates struct {
	Added   time.Time `json:"added,omitempty"`
	Updated time.Time `json:"updated,omitempty"`
}

var modDatesMu sync.Mutex

func modDatesPath() string { return join(dataDir, "mod-dates.json") }

func pakDateKey(key string) string     { return "pak:" + strings.ToLower(key) }
func scriptDateKey(name string) string { return "script:" + strings.ToLower(name) }

func loadModDates() map[string]modDates {
	m := map[string]modDates{}
	if b, err := os.ReadFile(modDatesPath()); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

// recordModDates notes that these mods were just added (or, when updated is
// true for them, updated).
func recordModDates(changes map[string]bool) {
	if len(changes) == 0 {
		return
	}
	modDatesMu.Lock()
	defer modDatesMu.Unlock()
	m := loadModDates()
	now := time.Now()
	for key, updated := range changes {
		d := m[key]
		if updated {
			d.Updated = now
			if d.Added.IsZero() {
				d.Added = now
			}
		} else {
			d = modDates{Added: now}
		}
		m[key] = d
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := modDatesPath() + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, modDatesPath())
	}
}

// datesFor gives the window's Added and Updated dates (nil when unknown).
func datesFor(m map[string]modDates, key string) (added, updated *time.Time) {
	d, ok := m[key]
	if !ok {
		return nil, nil
	}
	if !d.Added.IsZero() {
		a := d.Added
		added = &a
	}
	if !d.Updated.IsZero() {
		u := d.Updated
		updated = &u
	}
	return
}
