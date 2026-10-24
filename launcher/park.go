package main

// The Modded / Unmodded switch.
//
// Unmodded moves everything that changes the game out of the game folder into
// "<game> - mod storage\Switched off", keeping the same folder layout; Modded
// moves it back. Moves are renames on the same drive, so they are instant and
// nothing is copied. Every step is written to a journal first, and if a step
// fails the steps already done are undone, so the game is never left half
// switched. Nothing is ever deleted or overwritten: a copy that is in the way
// is set aside as "(older copy …)".

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type parkItem struct {
	Key   string
	Label string
	Rel   []string
}

var parkItems = []parkItem{
	{"dwmapi", "UE4SS starter (dwmapi.dll)", []string{"Game", "Binaries", "Win64", "dwmapi.dll"}},
	{"ue4ss", "UE4SS, the Mod Loader and all their mods", []string{"Game", "Binaries", "Win64", "ue4ss"}},
	{"tildemods", "The game's ~mods folder", []string{"Game", "Content", "Paks", "~mods"}},
	{"logicmods", "The game's LogicMods folder", []string{"Game", "Content", "Paks", "LogicMods"}},
}

type ItemState struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	InGame   bool   `json:"inGame"`
	Parked   bool   `json:"parked"`
	GamePath string `json:"gamePath"`
	ParkPath string `json:"parkPath"`
}

const (
	modeModded   = "modded"
	modeUnmodded = "unmodded"
	modeMixed    = "mixed"
	modeNone     = "none" // no mod files anywhere
)

type ModeInfo struct {
	Mode  string      `json:"mode"`
	Items []ItemState `json:"items"`
}

func (g Game) itemPaths(it parkItem) (string, string) {
	return join(g.Root, it.Rel...), join(g.OffDir(), it.Rel...)
}

func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func (g Game) modeInfo() ModeInfo {
	var mi ModeInfo
	anyGame, anyPark := false, false
	for _, it := range parkItems {
		gp, pp := g.itemPaths(it)
		st := ItemState{Key: it.Key, Label: it.Label, InGame: hasFiles(gp), Parked: hasFiles(pp), GamePath: gp, ParkPath: pp}
		anyGame = anyGame || st.InGame
		anyPark = anyPark || st.Parked
		mi.Items = append(mi.Items, st)
	}
	switch {
	case anyGame && anyPark:
		mi.Mode = modeMixed
	case anyGame:
		mi.Mode = modeModded
	case anyPark:
		mi.Mode = modeUnmodded
	default:
		mi.Mode = modeNone
	}
	return mi
}

// hasFiles reports whether p is a file, or a folder with at least one file
// somewhere inside it. Empty folders that Windows would not let go of during a
// switch (because a program had them open) count as gone.
func hasFiles(p string) bool {
	st, err := os.Lstat(p)
	if err != nil {
		return false
	}
	if !st.IsDir() {
		return true
	}
	found := false
	filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// liveOrParked is where one of the switched folders is right now: in the game,
// or switched off.
func (g Game) liveOrParked(rel ...string) string {
	gp, pp := join(g.Root, rel...), join(g.OffDir(), rel...)
	switch {
	case hasFiles(gp):
		return gp
	case hasFiles(pp):
		return pp
	case isDir(gp):
		return gp
	case isDir(pp):
		return pp
	}
	return gp
}

// ue4ssRoot is where the ue4ss folder is right now: in the game, or switched off.
func (g Game) ue4ssRoot() string { return g.liveOrParked("Game", "Binaries", "Win64", "ue4ss") }

func (g Game) loaderDir() string { return join(g.ue4ssRoot(), "Mods", "IoStoreLoaderMod") }

func (g Game) tildeModsDir() string { return g.liveOrParked("Game", "Content", "Paks", "~mods") }

// ---- moving ------------------------------------------------------------------

// rename is a variable so the tests can make it fail on purpose.
var rename = os.Rename

func moveItem(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	err := rename(src, dst)
	// Antivirus scanners and Explorer's thumbnail cache hold files for a moment: try a few more times.
	for wait := 150 * time.Millisecond; err != nil && wait <= 1200*time.Millisecond && !isCrossDevice(err) && lexists(src) && !lexists(dst); wait *= 2 {
		if !(isSharingViolation(err) || errors.Is(err, os.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "access is denied")) {
			break
		}
		time.Sleep(wait)
		err = rename(src, dst)
	}
	if err == nil {
		return nil
	}
	if isCrossDevice(err) {
		return moveAcrossDrives(src, dst)
	}
	return err
}

// moveAcrossDrives is for a game folder that is a link to another drive. It is
// all or nothing: the copy is made first, then the original is renamed out of the
// way in one step (so it can't be left half there), and only then deleted.
func moveAcrossDrives(src, dst string) error {
	if err := copyTree(src, dst); err != nil {
		os.RemoveAll(dst)
		return err
	}
	old := asideName(src, isDir(src))
	if err := rename(src, old); err != nil {
		os.RemoveAll(dst)
		return err
	}
	if err := os.RemoveAll(old); err != nil {
		logf("moved %s to %s, but the old copy %s could not be deleted: %v", src, dst, old, err)
	}
	return nil
}

func copyTree(src, dst string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return copyFile(src, dst, st.Mode())
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	ents, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if err := copyTree(join(src, e.Name()), join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies a file and keeps its date (the Mod Loader picks the newest of
// two same-named files, so dates matter).
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	var mtime time.Time
	if st, err := in.Stat(); err == nil {
		mtime = st.ModTime()
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".partial"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if !mtime.IsZero() {
		os.Chtimes(tmp, mtime, mtime)
	}
	os.Remove(dst)
	return os.Rename(tmp, dst)
}

// isLockError: Windows refused because a program has the file, or something
// inside the folder, open.
func isLockError(err error) bool {
	return err != nil && (isSharingViolation(err) || errors.Is(err, os.ErrPermission) ||
		strings.Contains(strings.ToLower(err.Error()), "access is denied") ||
		strings.Contains(strings.ToLower(err.Error()), "used by another process"))
}

// moveErr names the exact file or folder that could not be moved.
type moveErr struct {
	path string
	err  error
}

func (e *moveErr) Error() string { return e.err.Error() }
func (e *moveErr) Unwrap() error { return e.err }

// moveInto moves src to dst. A folder is moved in one go when Windows allows it.
// Windows refuses to move a folder while a program has anything inside it open
// (an Explorer window, an editor, a command window, an app watching the
// folder), so then its contents are moved one by one instead, and only the
// empty folder that is held open stays behind. Each move goes through step, so
// it is written to the journal and can be undone.
func moveInto(src, dst string, step func(from, to string) error) error {
	if !lexists(dst) {
		err := step(src, dst)
		if err == nil {
			return nil
		}
		if !isDir(src) || !isLockError(err) {
			return &moveErr{src, err}
		}
	} else if !isDir(dst) || !isDir(src) {
		return &moveErr{dst, errors.New("something with the same name is in the way")}
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return &moveErr{dst, err}
	}
	ents, err := os.ReadDir(src)
	if err != nil {
		return &moveErr{src, err}
	}
	for _, e := range ents {
		if err := moveInto(join(src, e.Name()), join(dst, e.Name()), step); err != nil {
			return err
		}
	}
	os.Remove(src) // empty now; if a program still holds it open, it stays, which does no harm
	return nil
}

// asideName gives "dwmapi (older copy 2026-10-14 2216).dll" or "ue4ss (older copy …)",
// adding " 2", " 3" … if that name is taken.
func asideName(p string, isDirectory bool) string {
	dir, base := filepath.Split(p)
	ext := ""
	if !isDirectory {
		ext = filepath.Ext(base)
		base = strings.TrimSuffix(base, ext)
	}
	stamp := time.Now().Format("2006-01-02 1504")
	for n := 1; ; n++ {
		suffix := ""
		if n > 1 {
			suffix = fmt.Sprintf(" %d", n)
		}
		c := filepath.Join(dir, base+" (older copy "+stamp+suffix+")"+ext)
		if !lexists(c) {
			return c
		}
	}
}

type moveStep struct {
	From string `json:"from"`
	To   string `json:"to"`
	Note string `json:"note,omitempty"`
}

type switchJournal struct {
	To       string     `json:"to"`
	Started  time.Time  `json:"started"`
	Steps    []moveStep `json:"steps"`
	Finished bool       `json:"finished"`
}

func (g Game) journalPath() string { return join(g.Storage(), "switch-journal.json") }

func (g Game) writeJournal(j *switchJournal) {
	os.MkdirAll(g.Storage(), 0o755)
	b, _ := json.MarshalIndent(j, "", "  ")
	os.WriteFile(g.journalPath(), b, 0o644)
}

type SwitchResult struct {
	Mode  string   `json:"mode"`
	Moved []string `json:"moved"`
	Aside []string `json:"aside"` // copies that were in the way, kept in the storage folder
}

var errGameRunning = errors.New("the game is running. Close Ace Combat 8 first, then try again")

// switchTo moves the mod files so the game ends up modded or unmodded.
func (g Game) switchTo(target string) (SwitchResult, error) {
	res := SwitchResult{}
	if target != modeModded && target != modeUnmodded {
		return res, errors.New("unknown mode " + target)
	}
	if gameRunning() {
		return res, errGameRunning
	}
	writeStorageReadme(g)
	j := &switchJournal{To: target, Started: time.Now()}
	g.writeJournal(j)

	undo := func() []error {
		var errs []error
		for i := len(j.Steps) - 1; i >= 0; i-- {
			s := j.Steps[i]
			if err := moveItem(s.To, s.From); err != nil {
				errs = append(errs, fmt.Errorf("could not move %s back to %s: %v", s.To, s.From, err))
			}
		}
		if len(errs) == 0 {
			// everything is back where it was: nothing is left to finish
			j.Steps, j.Finished = nil, true
			g.writeJournal(j)
		}
		return errs
	}
	do := func(from, to, note string) error {
		if err := moveItem(from, to); err != nil {
			return err
		}
		j.Steps = append(j.Steps, moveStep{From: from, To: to, Note: note})
		g.writeJournal(j)
		return nil
	}

	step := func(from, to string) error { return do(from, to, "") }
	for _, it := range parkItems {
		gp, pp := g.itemPaths(it)
		src, dst := gp, pp
		if target == modeModded {
			src, dst = pp, gp
		}
		if !hasFiles(src) {
			continue
		}
		if hasFiles(dst) {
			// Something is already where this goes. Keep it, under another name in the storage folder.
			aside := asideName(pp, isDir(dst))
			if err := do(dst, aside, "set aside"); err != nil {
				return res, g.failSwitch(it, dst, err, undo)
			}
			res.Aside = append(res.Aside, aside)
		}
		if err := moveInto(src, dst, step); err != nil {
			where := src
			var me *moveErr
			if errors.As(err, &me) {
				where = me.path
			}
			return res, g.failSwitch(it, where, err, undo)
		}
		res.Moved = append(res.Moved, it.Label)
	}
	j.Finished = true
	g.writeJournal(j)
	// tidy up empty folders left behind (on either side)
	for _, it := range parkItems {
		gp, pp := g.itemPaths(it)
		for _, p := range []string{gp, pp} {
			if !hasFiles(p) {
				removeEmptyTree(p)
			}
		}
	}
	removeEmptyParents(join(g.OffDir(), "Game", "Binaries", "Win64"), g.OffDir())
	removeEmptyParents(join(g.OffDir(), "Game", "Content", "Paks"), g.OffDir())
	res.Mode = g.modeInfo().Mode
	logf("switched to %s: moved %v, set aside %v", target, res.Moved, res.Aside)
	return res, nil
}

func (g Game) failSwitch(it parkItem, path string, err error, undo func() []error) error {
	logf("switch failed at %s (%s): %v", it.Label, path, err)
	problems := undo()
	msg := fmt.Sprintf("%s would not move %s (%s). %s", sysName, filepath.Base(path), path, explainMoveError(err))
	if len(problems) > 0 {
		for _, p := range problems {
			logf("undo failed: %v", p)
		}
		msg += " The launcher could not put everything back either; press the switch again once the file is free."
	} else {
		msg += " Nothing was changed."
	}
	return errors.New(msg)
}

func explainMoveError(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case platform == "linux" && (errors.Is(err, os.ErrPermission) || strings.Contains(s, "read-only")):
		return "Your user account isn't allowed to change it. Check that the drive isn't read-only and that the folder belongs to you."
	case strings.Contains(s, "used by another process") || strings.Contains(s, "sharing violation") || isSharingViolation(err):
		return "A program is using it: close the game, any Explorer window showing that folder, and tools such as Skin Studio or a text editor with a log file open."
	case strings.Contains(s, "access is denied") || errors.Is(err, os.ErrPermission):
		return "A program has it open. Close the game, Explorer windows showing that folder, editors such as VS Code or Notepad++, and command windows, then try again. If it keeps happening, restart the PC."
	case strings.Contains(s, "not enough space") || strings.Contains(s, "no space"):
		return "The drive is full."
	}
	return "(" + err.Error() + ")"
}

// removeEmptyTree deletes a folder that holds only empty folders. Folders a
// program still has open stay.
func removeEmptyTree(dir string) {
	if !isDir(dir) || hasFiles(dir) {
		return
	}
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if e.IsDir() {
			removeEmptyTree(join(dir, e.Name()))
		}
	}
	os.Remove(dir)
}

func removeEmptyParents(dir, stop string) {
	stop = filepath.Clean(stop)
	for d := filepath.Clean(dir); d != stop && strings.HasPrefix(d, stop); d = filepath.Dir(d) {
		if err := os.Remove(d); err != nil { // only removes empty folders
			return
		}
	}
}

func writeStorageReadme(g Game) {
	p := join(g.Storage(), "README - what is this folder.txt")
	if exists(p) {
		return
	}
	os.MkdirAll(g.Storage(), 0o755)
	text := `This folder belongs to ACE8 Mod Launcher.

"Switched off"  holds UE4SS, the Mod Loader and your mods while the launcher
                is set to Unmodded. Switch back to Modded in the launcher and
                they are moved back into the game.
"Removed mods"  holds mods you removed in the launcher. Restore them from the
                launcher's Mods page, or delete them there for good.

Please don't move things in or out of here by hand while the launcher is open.

` + contact + `
`
	os.WriteFile(p, []byte(strings.ReplaceAll(text, "\n", "\r\n")), 0o644)
}
