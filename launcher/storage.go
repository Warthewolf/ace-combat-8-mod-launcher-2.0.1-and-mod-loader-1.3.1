package main

// Choosing where the mod storage folder is.
//
// Everything in the old storage folder (switched-off mods, removed mods, the
// switch record) is moved to the new one, then the launcher uses the new one.
// If a move fails, everything already moved goes back, and the old folder stays
// in use. To another drive, things are copied first and the originals are
// deleted only once the new folder is in use, so a launcher that is stopped
// part way never leaves the only copy of a mod in a folder it doesn't look in.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type StorageResult struct {
	Path       string `json:"path"`
	Unchanged  bool   `json:"unchanged"`
	Moved      int    `json:"moved"`      // things moved from the old folder
	OtherDrive bool   `json:"otherDrive"` // the new folder is on another drive than the game
}

// movingHere is written into the new storage folder while a move to it is
// under way.
const movingHere = ".ace8-moving-here.json"

// storageMove is what movingHere holds: where the move comes from, and the
// copies made so far on another drive (their originals are still in From).
type storageMove struct {
	From   string     `json:"from"`
	Copies []moveStep `json:"copies"`
}

func writeStorageMove(target string, m storageMove) {
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(join(target, movingHere), b, 0o644); err != nil {
		logf("storage move: could not write %s: %v", join(target, movingHere), err)
	}
}

// looksLikeStorage: a folder that already is a mod storage folder.
func looksLikeStorage(p string) bool {
	for _, n := range []string{"Switched off", "Removed mods", "README - what is this folder.txt", "switch-journal.json", movingHere} {
		if lexists(join(p, n)) {
			return true
		}
	}
	return false
}

// launcherTemp: the launcher's own temporary files and folders, never moved along.
func launcherTemp(name string) bool {
	for _, n := range []string{copyingDir, deletingDir, movingHere, ".ace8-write-test", "switch-journal.json.tmp"} {
		if strings.EqualFold(name, n) {
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
	if g.unfinishedSwitch() != "" {
		return res, errors.New("the last switch did not finish. Press Modded or Unmodded on the Play page first, then move the storage folder")
	}
	if g.storageMissing() {
		return g.leaveMissingStorage(old, target, toDefault, res)
	}
	ents, err := os.ReadDir(old)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, fmt.Errorf("%s won't let the launcher read the storage folder %s (%v). The storage folder was not changed", sysName, old, err)
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
	// a move into the folder in use now was finished (only deleting the old
	// copies may have been cut short), so its note is not needed any more
	os.Remove(join(old, movingHere))
	again := clearStoppedMove(old, target)
	if !again && hasFiles(join(old, "Switched off")) && hasFiles(join(target, "Switched off")) {
		return res, errors.New("that folder already holds switched-off mods from another storage folder. Choose an empty folder")
	}
	sweepLeftovers(target, join(target, "Removed mods"), join(target, "Switched off"))

	// move everything over; remember each move so it can be undone
	type mv struct {
		from, to string
		copied   bool // to another drive: the original is still in the old folder
	}
	var done []mv
	rec := storageMove{From: old}
	writeStorageMove(target, rec)
	step := func(from, to string) error {
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		err := renameRetrying(from, to, false)
		if err == nil {
			done = append(done, mv{from, to, false})
			return nil
		}
		if !isCrossDevice(err) {
			return err
		}
		if err := placeCopy(from, to); err != nil {
			return err
		}
		done = append(done, mv{from, to, true})
		rec.Copies = append(rec.Copies, moveStep{From: from, To: to})
		writeStorageMove(target, rec)
		return nil
	}
	// undo returns what could not be moved back
	undo := func() []string {
		var left []string
		for i := len(done) - 1; i >= 0; i-- {
			d := done[i]
			if d.copied {
				removeRetrying(d.to) // only the copy: the original never left the old folder
				continue
			}
			if err := moveItem(d.to, d.from); err != nil {
				logf("storage move: could not put %s back: %v", d.to, err)
				left = append(left, filepath.Base(d.to))
			}
		}
		if len(left) == 0 {
			os.Remove(join(target, movingHere))
		}
		return left
	}
	fail := func(msg string) (StorageResult, error) {
		if left := undo(); len(left) > 0 {
			return res, fmt.Errorf("%s Some of it could not be moved back and is still in %s: %s. Choose that folder again once nothing is using it, so the rest follows", msg, target, strings.Join(left, ", "))
		}
		return res, errors.New(msg + " The storage folder was not changed")
	}
	moveFail := func(what string, err error) (StorageResult, error) {
		where := what
		var me *moveErr
		if errors.As(err, &me) {
			where = me.path
		}
		return fail(fmt.Sprintf("%s would not move %s (%s). %s", sysName, filepath.Base(where), where, explainMoveError(err)))
	}
	for _, e := range ents {
		src, dst := join(old, e.Name()), join(target, e.Name())
		switch {
		case launcherTemp(e.Name()):
			continue
		case strings.EqualFold(e.Name(), "Removed mods") && e.IsDir():
			// each removed mod keeps its own folder; same names get a number
			subs, _ := os.ReadDir(src)
			for _, s := range subs {
				if launcherTemp(s.Name()) {
					continue
				}
				d := join(dst, s.Name())
				for n := 2; lexists(d); n++ {
					d = join(dst, fmt.Sprintf("%s %d", s.Name(), n))
				}
				if err := moveInto(join(src, s.Name()), d, step); err != nil {
					return moveFail(join(src, s.Name()), err)
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
				return moveFail(src, err)
			}
			if hasFiles(dst) {
				res.Moved++
			}
		}
	}

	before := getSettings()
	if err := updateSettings(func(s *Settings) {
		if toDefault {
			s.StorageDir, s.StorageGame = "", ""
		} else {
			s.StorageDir, s.StorageGame = target, g.Root
		}
	}); err != nil {
		// updateSettings has already changed the settings in memory: put them back,
		// so the launcher goes on using the old folder that everything goes back to
		updateSettings(func(s *Settings) { s.StorageDir, s.StorageGame = before.StorageDir, before.StorageGame })
		return fail(fmt.Sprintf("the launcher could not save its settings (%v).", err))
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
	// the new folder is in use: the originals of what was copied are not needed any more
	for _, d := range done {
		if d.copied {
			if trash, err := renameAway(d.from); err == nil {
				removeRetrying(trash)
			} else {
				logf("storage move: the old copy %s could not be deleted: %v", d.from, err)
			}
		}
	}
	os.Remove(join(target, movingHere))
	os.Remove(join(old, "README - what is this folder.txt"))
	removeEmptyTree(join(old, "Adding"))
	sweepLeftovers(old, join(old, "Removed mods"), join(old, "Switched off"))
	removeEmptyTree(old)
	writeStorageReadme(g)
	logf("storage folder moved from %s to %s (%d moved)", old, target, res.Moved)
	return res, nil
}

// clearStoppedMove: a move from old to target that was stopped part way (the
// launcher was closed during a long copy to another drive) left copies in
// target while their originals stayed in old. They are cleared away so the
// move can simply start again. It reports whether there was such a move.
func clearStoppedMove(old, target string) bool {
	b, err := os.ReadFile(join(target, movingHere))
	if err != nil {
		return false
	}
	var m storageMove
	if json.Unmarshal(b, &m) != nil || !samePath(m.From, old) {
		return false
	}
	for _, c := range m.Copies {
		if lexists(c.From) && isInside(c.To, target) && !samePath(c.To, target) {
			removeRetrying(c.To)
		}
	}
	os.Remove(join(target, movingHere))
	logf("storage move: carrying on with a move from %s to %s that was stopped part way", old, target)
	return true
}

// leavingMissing remembers when the player was told that their switched-off
// and removed mods are in a storage folder that can't be found.
var leavingMissing struct {
	from, to string
	at       time.Time
}

// leaveMissingStorage changes the storage folder while the chosen one can't be
// found (a drive that isn't connected, a folder moved or renamed by hand), so
// nothing can be moved from it. A folder the player picks that holds mods (the
// same folder in its new place, or its drive back under another letter) is
// simply used. Otherwise the player is first told where their mods are, and
// only pressing the same button again, after reading that, goes on without them.
func (g Game) leaveMissingStorage(old, target string, toDefault bool, res StorageResult) (StorageResult, error) {
	if toDefault || !holdsMods(target) {
		lm := leavingMissing
		if !samePath(lm.from, old) || !samePath(lm.to, target) || time.Since(lm.at) < 3*time.Second || time.Since(lm.at) > 10*time.Minute {
			leavingMissing.from, leavingMissing.to, leavingMissing.at = old, target, time.Now()
			return res, fmt.Errorf("your switched-off and removed mods are in %s, which can't be found. Connect the drive it is on first, so they come along. If that folder is gone for good, press the same button again to carry on without them (if it turns up again, they are still in it)", old)
		}
	}
	leavingMissing.from, leavingMissing.to = "", ""
	if err := os.MkdirAll(target, 0o755); err != nil {
		return res, fmt.Errorf("%s won't let the launcher make that folder (%v). Choose another folder", sysName, err)
	}
	if err := updateSettings(func(s *Settings) {
		if toDefault {
			s.StorageDir, s.StorageGame = "", ""
		} else {
			s.StorageDir, s.StorageGame = target, g.Root
		}
	}); err != nil {
		return res, fmt.Errorf("the launcher could not save its settings (%v). The storage folder was not changed", err)
	}
	writeStorageReadme(g)
	logf("storage folder changed from %s, which can't be found, to %s; nothing could be moved from it", old, target)
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
