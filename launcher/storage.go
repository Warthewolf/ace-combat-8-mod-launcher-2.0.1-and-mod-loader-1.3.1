package main

// Choosing where the mod storage folder is.
//
// Everything in the old storage folder (switched-off mods, removed mods, the
// switch record) is moved to the new one, then the launcher uses the new one.
// If a move fails, everything already moved goes back, and the old folder stays
// in use.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type StorageResult struct {
	Path       string `json:"path"`
	Unchanged  bool   `json:"unchanged"`
	Moved      int    `json:"moved"`      // things moved from the old folder
	OtherDrive bool   `json:"otherDrive"` // the new folder is on another drive than the game
}

// looksLikeStorage: a folder that already is a mod storage folder.
func looksLikeStorage(p string) bool {
	for _, n := range []string{"Switched off", "Removed mods", "README - what is this folder.txt", "switch-journal.json"} {
		if lexists(join(p, n)) {
			return true
		}
	}
	return false
}

// storageTarget turns the folder the player picked into the storage folder:
// an empty folder, or one that already is a storage folder, is used as it is;
// in any other folder, a storage folder is made inside it.
func (g Game) storageTarget(picked string) string {
	picked = filepath.Clean(picked)
	if ents, err := os.ReadDir(picked); err == nil && len(ents) == 0 {
		return picked
	}
	if looksLikeStorage(picked) || strings.EqualFold(filepath.Base(picked), g.storageName()) {
		return picked
	}
	return join(picked, g.storageName())
}

// moveStorage makes target the storage folder ("" = back to the default).
func (g Game) moveStorage(target string) (StorageResult, error) {
	var res StorageResult
	old := g.Storage()
	toDefault := target == ""
	if toDefault {
		target = g.DefaultStorage()
	}
	target = filepath.Clean(target)
	if abs, err := filepath.Abs(target); err == nil {
		target = abs
	}
	res.Path, res.OtherDrive = target, !sameDrive(target, g.Root)
	if samePath(target, old) {
		res.Unchanged = true
		return res, nil
	}
	switch {
	case isInside(target, g.Root):
		return res, errors.New("the storage folder can't be inside the game folder: the game would load the mods in it. Choose a folder outside ACE COMBAT 8")
	case isInside(target, old):
		return res, errors.New("that folder is inside the current storage folder. Choose a folder somewhere else")
	case isInside(old, target):
		return res, errors.New("that folder holds the current storage folder. Choose an empty folder, or a folder somewhere else")
	case filepath.Dir(target) == target:
		return res, errors.New("choose a folder, not a whole drive")
	}
	if b, err := os.ReadFile(g.journalPath()); err == nil {
		var j switchJournal
		if json.Unmarshal(b, &j) == nil && !j.Finished && len(j.Steps) > 0 {
			return res, errors.New("the last switch did not finish. Press Modded or Unmodded on the Play page first, then move the storage folder")
		}
	}
	// can the launcher write there?
	if err := os.MkdirAll(target, 0o755); err != nil {
		return res, fmt.Errorf("%s won't let the launcher make that folder (%v). Choose another folder", sysName, err)
	}
	probe := join(target, ".ace8-write-test")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		return res, fmt.Errorf("%s won't let the launcher write in that folder. Choose another folder, for example one in Documents or on the game's drive", sysName)
	}
	os.Remove(probe)
	if hasFiles(join(old, "Switched off")) && hasFiles(join(target, "Switched off")) {
		return res, errors.New("that folder already holds switched-off mods from another storage folder. Choose an empty folder")
	}

	// move everything over; remember each move so it can be undone
	type mv struct{ from, to string }
	var done []mv
	step := func(from, to string) error {
		if err := moveItem(from, to); err != nil {
			return err
		}
		done = append(done, mv{from, to})
		return nil
	}
	undo := func() {
		for i := len(done) - 1; i >= 0; i-- {
			if err := moveItem(done[i].to, done[i].from); err != nil {
				logf("storage move: could not put %s back: %v", done[i].to, err)
			}
		}
	}
	fail := func(what string, err error) (StorageResult, error) {
		undo()
		where := what
		var me *moveErr
		if errors.As(err, &me) {
			where = me.path
		}
		return res, fmt.Errorf("%s would not move %s (%s). %s The storage folder was not changed", sysName, filepath.Base(where), where, explainMoveError(err))
	}
	ents, _ := os.ReadDir(old)
	for _, e := range ents {
		src, dst := join(old, e.Name()), join(target, e.Name())
		switch {
		case strings.EqualFold(e.Name(), "Removed mods") && e.IsDir():
			// each removed mod keeps its own folder; same names get a number
			subs, _ := os.ReadDir(src)
			for _, s := range subs {
				d := join(dst, s.Name())
				for n := 2; lexists(d); n++ {
					d = join(dst, fmt.Sprintf("%s %d", s.Name(), n))
				}
				if err := moveInto(join(src, s.Name()), d, step); err != nil {
					return fail(join(src, s.Name()), err)
				}
				res.Moved++
			}
		case strings.EqualFold(e.Name(), "README - what is this folder.txt"), strings.EqualFold(e.Name(), "Adding"):
			continue // made again where needed
		case lexists(dst) && strings.EqualFold(e.Name(), "switch-journal.json"):
			os.Remove(src) // a finished switch record; the new folder's own one is kept
			continue
		default:
			if err := moveInto(src, dst, step); err != nil {
				return fail(src, err)
			}
			if hasFiles(dst) {
				res.Moved++
			}
		}
	}

	// removed mods remember where they came from: point them at the new folder
	if subs, err := os.ReadDir(join(target, "Removed mods")); err == nil {
		for _, s := range subs {
			p := join(target, "Removed mods", s.Name(), "removed.json")
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			var ri removedInfo
			if json.Unmarshal(b, &ri) != nil {
				continue
			}
			ri.From = rebase(ri.From, old, target)
			for i := range ri.Files {
				ri.Files[i] = rebase(ri.Files[i], old, target)
			}
			writeRemovedInfo(filepath.Dir(p), ri)
		}
	}

	if err := updateSettings(func(s *Settings) {
		if toDefault {
			s.StorageDir, s.StorageGame = "", ""
		} else {
			s.StorageDir, s.StorageGame = target, g.Root
		}
	}); err != nil {
		undo()
		return res, err
	}
	os.Remove(join(old, "README - what is this folder.txt"))
	removeEmptyTree(join(old, "Adding"))
	removeEmptyTree(old)
	writeStorageReadme(g)
	logf("storage folder moved from %s to %s (%d moved)", old, target, res.Moved)
	return res, nil
}

// rebase changes a path under from into the same path under to.
func rebase(p, from, to string) string {
	if isInside(p, from) {
		rel, err := filepath.Rel(from, p)
		if err == nil {
			return join(to, rel)
		}
	}
	return p
}
