package main

// When each mod was added to the game, or last updated, through the launcher.
// Kept in mod-dates.json in the launcher's data folder, by the mod's name, so it
// stays the same while the game is switched to Unmodded.

import (
	"encoding/json"
	"errors"
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

// loadModDates reads the dates (none when the file can't be read).
func loadModDates() map[string]modDates {
	modDatesMu.Lock()
	defer modDatesMu.Unlock()
	m, _ := readModDates()
	return m
}

// readModDates reads mod-dates.json. A missing file is no dates yet; a file that
// can't be read right now gives an error, so it is not overwritten by an empty list.
func readModDates() (map[string]modDates, error) {
	m := map[string]modDates{}
	b, err := os.ReadFile(modDatesPath())
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return map[string]modDates{}, errDamagedDates
	}
	return m, nil
}

var errDamagedDates = errors.New("mod-dates.json is damaged")

// recordModDates notes that these mods were just added (or, when updated is
// true for them, updated). carried names, for a mod updated under a new name,
// the name it had, so it keeps the date it was first added.
func recordModDates(changes map[string]bool, carried map[string]string) {
	if len(changes) == 0 {
		return
	}
	modDatesMu.Lock()
	defer modDatesMu.Unlock()
	m, err := readModDates()
	switch {
	case errors.Is(err, errDamagedDates):
		// keep the damaged file for a look, and start a new list
		os.Remove(modDatesPath() + ".bad")
		if rerr := os.Rename(modDatesPath(), modDatesPath()+".bad"); rerr != nil {
			logf("mod-dates.json is damaged and could not be kept aside (%v); the dates of this add were not saved", rerr)
			return
		}
		logf("mod-dates.json was damaged; kept as mod-dates.json.bad, and a new one started")
	case err != nil:
		logf("could not read mod-dates.json (%v); the dates of this add were not saved", err)
		return
	}
	now := time.Now()
	for key, updated := range changes {
		d := m[key]
		if from, ok := carried[key]; ok && d.Added.IsZero() {
			d.Added = m[from].Added
		}
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
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		logf("could not save mod-dates.json: %v", err)
		return
	}
	// Windows refuses to replace a file another program (a virus scanner) has
	// open for a moment: try a few times
	for wait := 50 * time.Millisecond; ; wait *= 2 {
		err = os.Rename(tmp, modDatesPath())
		if err == nil || wait > 800*time.Millisecond {
			break
		}
		time.Sleep(wait)
	}
	if err != nil {
		os.Remove(tmp)
		logf("could not save mod-dates.json: %v", err)
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
