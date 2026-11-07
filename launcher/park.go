package main

// The Modded / Unmodded switch.
//
// Unmodded moves everything that changes the game out of the game folder into
// "<game> - mod storage\Switched off", keeping the same folder layout; Modded
// moves it back. Moves are renames on the same drive, so they are instant and
// nothing is copied. Every step is written to a journal first, and if a step
// fails the steps already done are undone, so the game is never left half
// switched. A switch that stopped part way (the launcher was closed, the PC went
// off) is finished or put back from the journal before the next one starts.
// Nothing is ever deleted or overwritten: a folder that is on both sides is
// merged, and a file that is in the way is set aside as "(older copy …)" in the
// storage folder.

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
	return findCase(join(g.Root, it.Rel...)), findCase(join(g.OffDir(), it.Rel...))
}

// isFile: dwmapi.dll is one file; the other items are folders.
func (it parkItem) isFile() bool { return filepath.Ext(it.Rel[len(it.Rel)-1]) != "" }

// findCase finds p even when its last part is spelled with other capitals. On
// Linux "~Mods" and "~mods" are two different folders, but the game (through
// Proton) loads paks from both, so a "~Mods" folder made by hand must be found
// and switched off too. On Windows the plain path is always found first.
func findCase(p string) string {
	if lexists(p) {
		return p
	}
	dir, base := filepath.Split(p)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return p
	}
	for _, e := range ents {
		if strings.EqualFold(e.Name(), base) {
			return join(dir, e.Name())
		}
	}
	return p
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
	gp, pp := findCase(join(g.Root, rel...)), findCase(join(g.OffDir(), rel...))
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
	err := renameRetrying(src, dst, false)
	if err == nil {
		return nil
	}
	if isCrossDevice(err) {
		return moveAcrossDrives(src, dst)
	}
	return err
}

// renameRetrying renames src to dst. Antivirus scanners and Explorer's
// thumbnail cache hold files for a moment: then it tries a few more times.
// replacing: dst is an existing file that is meant to be replaced.
func renameRetrying(src, dst string, replacing bool) error {
	err := rename(src, dst)
	for wait := 150 * time.Millisecond; err != nil && wait <= 1200*time.Millisecond && !isCrossDevice(err) && lexists(src) && (replacing || !lexists(dst)); wait *= 2 {
		if !(isSharingViolation(err) || errors.Is(err, os.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "access is denied")) {
			break
		}
		time.Sleep(wait)
		err = rename(src, dst)
	}
	return err
}

// Names of the launcher's own temporary folders: copies that are not complete
// yet, and originals that are being deleted because they were copied.
const (
	copyingDir  = ".ace8-copying"
	deletingDir = ".ace8-deleting"
)

// moveAcrossDrives moves src to another drive (a storage folder on another
// drive, or a game folder that is a link to another drive). It is all or
// nothing. The copy is made under a temporary name and gets its real name only
// once it is complete, so a launcher that is stopped part way (or a PC that
// goes off) never leaves half a copy where it would be taken for the real
// thing. Then the original is renamed out of the way in one step, so it can't
// be left half there, and only then deleted. Both temporary places are outside
// Paks and ue4ss, where neither the game nor UE4SS would load what is left.
func moveAcrossDrives(src, dst string) error {
	if err := placeCopy(src, dst); err != nil {
		return err
	}
	trash, err := renameAway(src)
	if err != nil {
		os.RemoveAll(dst) // only the copy: the original is still there
		return err
	}
	removeRetrying(trash)
	return nil
}

// placeCopy copies src to dst through a temporary copy.
func placeCopy(src, dst string) error {
	var err error
	for i, holder := range []string{holdingDir(dst), filepath.Dir(dst)} {
		if i > 0 && samePath(holder, holdingDir(dst)) {
			break
		}
		tmp := join(holder, copyingDir, filepath.Base(dst))
		os.RemoveAll(tmp) // a leftover from a launcher that was stopped
		if err = copyTree(src, tmp); err == nil {
			err = renameRetrying(tmp, dst, false)
		}
		os.RemoveAll(tmp) // nothing is left there when the rename worked
		os.Remove(join(holder, copyingDir))
		if err == nil || !isCrossDevice(err) {
			return err
		}
		// that folder is on another drive than dst (a folder that is a link to
		// another drive): make the copy right next to dst instead
	}
	return err
}

// renameAway renames src into the launcher's folder for things being deleted
// and returns its new name.
func renameAway(src string) (string, error) {
	var err error
	for i, holder := range []string{holdingDir(src), filepath.Dir(src)} {
		if i > 0 && samePath(holder, holdingDir(src)) {
			break
		}
		trash := join(holder, deletingDir, filepath.Base(src))
		for n := 2; lexists(trash); n++ {
			trash = join(holder, deletingDir, fmt.Sprintf("%s %d", filepath.Base(src), n))
		}
		if err = os.MkdirAll(filepath.Dir(trash), 0o755); err == nil {
			if err = renameRetrying(src, trash, false); err == nil {
				return trash, nil
			}
		}
		os.Remove(join(holder, deletingDir))
		if !isCrossDevice(err) {
			return "", err
		}
	}
	return "", err
}

// removeRetrying deletes something the launcher has finished with, trying
// again for a moment while a program still has a file in it open. Whatever
// can't be deleted is cleared away at the next switch.
func removeRetrying(p string) {
	err := os.RemoveAll(p)
	for wait := 150 * time.Millisecond; err != nil && wait <= 1200*time.Millisecond; wait *= 2 {
		time.Sleep(wait)
		err = os.RemoveAll(p)
	}
	if err != nil {
		logf("could not delete %s yet: %v", p, err)
		return
	}
	os.Remove(filepath.Dir(p)) // the launcher's folder, if it is empty now
}

// holdingDir is the folder for the temporary copies of p: next to the Paks or
// ue4ss folder when p is inside one (the game loads every pak anywhere under
// Paks, and UE4SS every mod folder in ue4ss\Mods), otherwise the folder p is in.
func holdingDir(p string) string {
	out := filepath.Dir(p)
	for d := out; ; {
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		b, pb := strings.ToLower(filepath.Base(d)), strings.ToLower(filepath.Base(parent))
		if (b == "paks" && pb == "content") || (b == "ue4ss" && pb == "win64") {
			out = parent
		}
		d = parent
	}
	return out
}

// sweepLeftovers deletes what a launcher that was stopped during a move to
// another drive left behind in dirs: copies that were not finished (their
// originals are still in place) and originals that had already been copied.
func sweepLeftovers(dirs ...string) {
	for _, d := range dirs {
		for _, n := range []string{copyingDir, deletingDir} {
			if p := join(d, n); lexists(p) {
				logf("clearing away %s, left by a move that did not finish", p)
				os.RemoveAll(p)
			}
		}
	}
}

// sweepLeftovers in the game folder and in its switched-off copy.
func (g Game) sweepLeftovers() {
	var dirs []string
	for _, base := range []string{g.Root, g.OffDir()} {
		dirs = append(dirs, join(base, "Game", "Content"), join(base, "Game", "Content", "Paks"), join(base, "Game", "Binaries", "Win64"))
	}
	sweepLeftovers(dirs...)
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
// two same-named files, so dates matter). The copy is written next to dst and
// then renamed over it, which replaces the old file in one step (on Windows
// too), so there is never a moment with neither the old nor the new file.
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
	if err := renameRetrying(tmp, dst, true); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
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
	// Pending is written before the move starts and cleared once it is done,
	// so after a crash the launcher knows which move may have been under way.
	Pending bool `json:"pending,omitempty"`
}

type switchJournal struct {
	To       string     `json:"to"`
	Started  time.Time  `json:"started"`
	Steps    []moveStep `json:"steps"`
	Finished bool       `json:"finished"`
}

func (g Game) journalPath() string { return join(g.Storage(), "switch-journal.json") }

// writeJournal saves the journal as a new file that is renamed over the old
// one, so a crash while it is being written never leaves half a journal.
func (g Game) writeJournal(j *switchJournal) error {
	if err := os.MkdirAll(g.Storage(), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(j, "", "  ")
	p := g.journalPath()
	if err := os.WriteFile(p+".tmp", b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(p+".tmp", p); err != nil {
		os.Remove(p + ".tmp")
		return os.WriteFile(p, b, 0o644)
	}
	return nil
}

func (g Game) readJournal() *switchJournal {
	b, err := os.ReadFile(g.journalPath())
	if err != nil {
		return nil
	}
	var j switchJournal
	if json.Unmarshal(b, &j) != nil {
		return nil
	}
	return &j
}

// unfinishedSwitch is the mode a switch that stopped part way was going to, or
// "" when there is none. A switch stops part way when the launcher is closed
// or the PC goes off during it, or when a step can't be undone.
func (g Game) unfinishedSwitch() string {
	j := g.readJournal()
	if j == nil || j.Finished {
		return ""
	}
	for _, s := range j.Steps {
		if !s.Pending || lexists(s.To) {
			return j.To
		}
	}
	return ""
}

// belongsHere: every step of j is inside this game folder or its storage
// folder. A journal written before the game was moved can't be followed.
func (g Game) belongsHere(j *switchJournal) bool {
	for _, s := range j.Steps {
		for _, p := range []string{s.From, s.To} {
			if !isInside(p, g.Root) && !isInside(p, g.Storage()) {
				return false
			}
		}
	}
	return true
}

// itemOf is the switched item p is in (or is), in the game or switched off.
func (g Game) itemOf(p string) (parkItem, bool) {
	for _, it := range parkItems {
		gp, pp := g.itemPaths(it)
		if isInside(p, gp) || isInside(p, pp) {
			return it, true
		}
	}
	return parkItem{}, false
}

// relInItem is where p is inside its item ("" for the item itself).
func (g Game) relInItem(it parkItem, p string) string {
	gp, pp := g.itemPaths(it)
	for _, base := range []string{gp, pp} {
		if r, ok := relInside(p, base); ok {
			return r
		}
	}
	return filepath.Base(p)
}

// relInside: p as a path inside base, ignoring letter case the way Windows does.
func relInside(p, base string) (string, bool) {
	p, base = filepath.Clean(p), filepath.Clean(base)
	if strings.EqualFold(p, base) {
		return "", true
	}
	pre := strings.TrimSuffix(base, string(filepath.Separator)) + string(filepath.Separator)
	if len(p) > len(pre) && strings.EqualFold(p[:len(pre)], pre) {
		return p[len(pre):], true
	}
	return "", false
}

// switcher does one switch: every move goes through its journal, and copies
// that are in the way are kept in the storage folder.
type switcher struct {
	g     Game
	j     *switchJournal
	res   *SwitchResult
	aside map[string]string // item key → its "(older copy …)" folder (or file) in the storage folder
}

func (s *switcher) save() {
	if err := s.g.writeJournal(s.j); err != nil {
		logf("could not save the switch journal: %v", err)
	}
}

// do moves from to to as one step of the journal. The step is written down
// before the move starts, so a crash during a long copy is noticed.
func (s *switcher) do(from, to string) error {
	s.j.Steps = append(s.j.Steps, moveStep{From: from, To: to, Pending: true})
	s.save()
	err := moveItem(from, to)
	if err != nil {
		s.j.Steps = s.j.Steps[:len(s.j.Steps)-1] // nothing was moved
	} else {
		s.j.Steps[len(s.j.Steps)-1].Pending = false
	}
	s.save()
	return err
}

// undo moves the journal's steps back, the last one first. Each step is taken
// off the journal as soon as it is back, so when one can't be moved back the
// journal still lists exactly what is left, and the next switch carries on
// from there. What is in the way at the old place is merged with, never replaced.
func (s *switcher) undo() error {
	for i := len(s.j.Steps) - 1; i >= 0; i-- {
		st := s.j.Steps[i]
		if lexists(st.To) {
			if err := s.moveBack(st.To, st.From); err != nil {
				return err
			}
		} else if !st.Pending {
			logf("switch journal: %s is not there any more, so nothing goes back to %s", st.To, st.From)
		}
		s.j.Steps = s.j.Steps[:i]
		s.save()
	}
	s.j.Steps, s.j.Finished = nil, true
	s.save()
	return nil
}

func (s *switcher) moveBack(to, from string) error {
	it, ok := s.g.itemOf(from)
	if !ok {
		return moveInto(to, from, moveItem)
	}
	return s.merge(it, to, from, moveItem)
}

// takeOverUnfinished deals with a switch that stopped part way, before a new
// one starts, so that nothing it moved is taken for a copy that is in the way.
// Going the same way, the new switch carries on with its journal; going the
// other way, its steps are put back first.
func (s *switcher) takeOverUnfinished(target string) error {
	g := s.g
	s.j = &switchJournal{To: target, Started: time.Now()}
	old := g.readJournal()
	if old == nil || old.Finished || len(old.Steps) == 0 {
		return nil
	}
	if !g.belongsHere(old) {
		kept := join(g.Storage(), "switch-journal (other folder).json")
		os.Remove(kept)
		os.Rename(g.journalPath(), kept)
		logf("the unfinished switch journal is for another game or storage folder, so it is not followed; kept as %s", kept)
		return nil
	}
	// A step that was under way is done if its result is there; if not, the
	// move never happened (a copy that was not finished was cleared away). If
	// both are there, a copy to another drive was finished but the original
	// not taken away yet: the original stays, and the copy is kept as an
	// older copy, so the two are never mixed.
	var steps []moveStep
	for _, st := range old.Steps {
		if st.Pending && !lexists(st.To) {
			continue
		}
		if st.Pending && lexists(st.From) {
			if it, ok := g.itemOf(st.To); ok && s.setAside(it, st.To, moveItem) == nil {
				continue
			}
		}
		st.Pending = false
		steps = append(steps, st)
	}
	old.Steps = steps
	logf("a switch to %s did not finish (%d steps); switching to %s now", old.To, len(steps), target)
	s.j = old
	if old.To == target {
		return nil
	}
	if err := s.undo(); err != nil {
		path := ""
		var me *moveErr
		if errors.As(err, &me) {
			path = me.path
		}
		logf("putting back the unfinished switch failed: %v", err)
		return fmt.Errorf("the last switch did not finish, and %s would not move %s (%s) back. %s Then press Modded or Unmodded again", sysName, filepath.Base(path), path, explainMoveError(err))
	}
	s.j = &switchJournal{To: target, Started: time.Now()}
	return nil
}

// merge moves src into dst. When dst is not there, that is a plain move; when
// it is a folder, src's files join it. The same file on both sides is a clash:
// the newer one stays (the Mod Loader also uses the newest of two same-named
// mods) and the other is kept in the item's "(older copy …)" folder in the
// storage folder, never inside the game. The files of one mod (the same name
// with other endings: .utoc, .ucas, .pak) stay or go together.
func (s *switcher) merge(it parkItem, src, dst string, step func(from, to string) error) error {
	if !lexists(dst) {
		return moveInto(src, dst, step)
	}
	if !isDir(src) || !isDir(dst) {
		return s.clash(it, src, dst, step)
	}
	ents, err := os.ReadDir(src)
	if err != nil {
		return &moveErr{src, err}
	}
	clashing := map[string]bool{} // mods (file names without their ending) with files on both sides
	for _, e := range ents {
		if !e.IsDir() && isFile(join(dst, e.Name())) {
			clashing[stem(e.Name())] = true
		}
	}
	settled := map[string]bool{}
	for _, e := range ents {
		sp, dp := join(src, e.Name()), join(dst, e.Name())
		st := stem(e.Name())
		var err error
		switch {
		case !e.IsDir() && clashing[st]:
			if !settled[st] {
				settled[st] = true
				err = s.clashMod(it, src, dst, st, step)
			}
		case !lexists(dp):
			err = moveInto(sp, dp, step)
		default:
			err = s.clash(it, sp, dp, step)
		}
		if err != nil {
			return err
		}
	}
	os.Remove(src) // empty now; if a program still holds it open, it stays, which does no harm
	return nil
}

// clash settles one name that is on both sides: two folders are merged, a
// folder wins over a stray file of the same name, and of two files the newer
// one stays.
func (s *switcher) clash(it parkItem, src, dst string, step func(from, to string) error) error {
	switch {
	case isDir(src) && isDir(dst):
		return s.merge(it, src, dst, step)
	case isDir(dst) || (!isDir(src) && !modTime(src).After(modTime(dst))):
		return s.setAside(it, src, step)
	}
	if err := s.setAside(it, dst, step); err != nil {
		return err
	}
	return moveInto(src, dst, step)
}

// clashMod settles a mod that has files in both folders: the side with the
// newest file keeps all of the mod's files, and the other side's files are kept
// in the older-copy folder, so a mod never ends up as a mix of two versions.
func (s *switcher) clashMod(it parkItem, srcDir, dstDir, st string, step func(from, to string) error) error {
	srcFiles, dstFiles := filesNamed(srcDir, st), filesNamed(dstDir, st)
	if !newest(srcFiles).After(newest(dstFiles)) {
		for _, f := range srcFiles {
			if err := s.setAside(it, f, step); err != nil {
				return err
			}
		}
		return nil
	}
	for _, f := range dstFiles {
		if err := s.setAside(it, f, step); err != nil {
			return err
		}
	}
	for _, f := range srcFiles {
		if err := moveInto(f, join(dstDir, filepath.Base(f)), step); err != nil {
			return err
		}
	}
	return nil
}

// setAside moves p, which is in the way, to the item's "(older copy …)" place
// in the storage folder, at the same place inside the item.
func (s *switcher) setAside(it parkItem, p string, step func(from, to string) error) error {
	_, pp := s.g.itemPaths(it)
	rel := s.g.relInItem(it, p)
	root := s.aside[it.Key]
	if root == "" || lexists(join(root, rel)) {
		root = asideName(pp, !it.isFile())
		s.aside[it.Key] = root
		s.res.Aside = append(s.res.Aside, root)
	}
	return moveInto(p, join(root, rel), step)
}

// filesNamed lists the files in dir whose name without its ending is st.
func filesNamed(dir, st string) []string {
	ents, _ := os.ReadDir(dir)
	var out []string
	for _, e := range ents {
		if !e.IsDir() && stem(e.Name()) == st {
			out = append(out, join(dir, e.Name()))
		}
	}
	return out
}

func stem(name string) string { return strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name))) }

func modTime(p string) time.Time {
	if st, err := os.Lstat(p); err == nil {
		return st.ModTime()
	}
	return time.Time{}
}

func newest(files []string) time.Time {
	var t time.Time
	for _, f := range files {
		if m := modTime(f); m.After(t) {
			t = m
		}
	}
	return t
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
	g.sweepLeftovers()
	s := &switcher{g: g, res: &res, aside: map[string]string{}}
	if err := s.takeOverUnfinished(target); err != nil {
		return res, err
	}
	if err := g.writeJournal(s.j); err != nil {
		return res, fmt.Errorf("the launcher can't write in the mod storage folder (%s). %s Nothing was changed", g.Storage(), explainMoveError(err))
	}
	writeStorageReadme(g)

	for _, it := range parkItems {
		gp, pp := g.itemPaths(it)
		src, dst := gp, pp
		if target == modeModded {
			src, dst = pp, gp
		}
		if !hasFiles(src) {
			continue
		}
		var err error
		if it.isFile() && lexists(dst) {
			// dwmapi.dll is on both sides: the one being switched wins, and the
			// other is kept as "dwmapi (older copy …).dll" in the storage folder
			if err = s.setAside(it, dst, s.do); err == nil {
				err = moveInto(src, dst, s.do)
			}
		} else {
			// a folder that is on both sides is merged, so nothing in it is hidden
			err = s.merge(it, src, dst, s.do)
		}
		if err != nil {
			where := src
			var me *moveErr
			if errors.As(err, &me) {
				where = me.path
			}
			return res, s.fail(it, where, err)
		}
		res.Moved = append(res.Moved, it.Label)
	}
	s.j.Finished = true
	s.save()
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

func (s *switcher) fail(it parkItem, path string, err error) error {
	logf("switch failed at %s (%s): %v", it.Label, path, err)
	msg := fmt.Sprintf("%s would not move %s (%s). %s", sysName, filepath.Base(path), path, explainMoveError(err))
	if uerr := s.undo(); uerr != nil {
		logf("undo failed: %v", uerr)
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
