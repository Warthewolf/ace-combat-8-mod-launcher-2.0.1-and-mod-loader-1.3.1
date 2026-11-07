package main

// Updating script mods and UE4SS's shared folder without losing anything.
//
// Updates replace the installed copy in place; nothing goes to Removed mods.
// Script mods: every file of the installed copy that the download doesn't have
// is kept: settings a mod wrote while the game ran, and files other mods put
// into its folder (add-ons for Simple Mod Settings, songs for an audio player). A mod can also list, in ace8-keep.txt, files the
// player's copy should win for even when the download has them (default
// settings files the player has changed).
//
// UE4SS's shared folder (ue4ss\Mods\shared) holds Lua libraries several mods use.
// It is never replaced as a whole: each file from the download is added, or
// overwrites the installed one when it differs. UE4SS's own shared files are
// never overwritten, because mods often bundle an older copy of them.

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// updatingSuffix marks an old copy set aside while its update is put in place.
const updatingSuffix = ".ace8-updating"

// incompleteSuffix marks, next to a mod folder, that its new copy is still going
// in. Moving a folder file by file (when a program holds it) takes a while; if
// the launcher is closed then, the folder holds only part of the new copy.
const incompleteSuffix = ".ace8-incomplete"

// sweepUpdating cleans up after a launcher that was closed in the middle of an
// update: an old copy whose new one is all in place is deleted; one whose new
// copy never arrived, or only arrived in part, is put back.
func sweepUpdating(dirs ...string) {
	for _, d := range dirs {
		var walk func(dir string, depth int)
		walk = func(dir string, depth int) {
			ents, _ := os.ReadDir(dir)
			for _, e := range ents {
				p := join(dir, e.Name())
				if strings.HasSuffix(e.Name(), updatingSuffix) {
					sweepOne(p)
					continue
				}
				if strings.HasSuffix(e.Name(), incompleteSuffix) {
					if !lexists(strings.TrimSuffix(p, incompleteSuffix) + updatingSuffix) {
						os.Remove(p) // no old copy to go back to: the new one stays as it is
					}
					continue
				}
				if e.IsDir() && depth > 0 {
					walk(p, depth-1)
				}
			}
		}
		// deep enough for a file taken out of a pack ("Pack\Skin\Skin_P.utoc")
		walk(d, maxSearchDepth)
	}
}

// sweepUpdates runs sweepUpdating on every place an update leaves its old copy:
// the Mod Loader's folder, ue4ss\Mods, ~mods and LogicMods, in the game and in
// the switched-off copy. Left there, the old copy would load next to the new
// one, or move along with a switch. Callers hold the busy lock.
func (g Game) sweepUpdates() {
	if g.Root == "" {
		return
	}
	for _, base := range []string{g.Root, g.OffDir()} {
		ue := join(base, "Game", "Binaries", "Win64", "ue4ss")
		paks := join(base, "Game", "Content", "Paks")
		sweepUpdating(join(ue, "Mods", "IoStoreLoaderMod"), join(ue, "Mods"), join(paks, "~mods"), join(paks, "LogicMods"))
	}
}

// sweepOne deals with one old copy p left aside by an unfinished update.
func sweepOne(p string) {
	orig := strings.TrimSuffix(p, updatingSuffix)
	marker := orig + incompleteSuffix
	switch {
	case lexists(orig) && lexists(marker):
		// only part of the new copy went in (its files are still in the download):
		// the complete old copy is the one to keep
		if err := os.RemoveAll(orig); err != nil {
			logf("could not take out the unfinished new copy %s: %v", orig, err)
			return
		}
		if err := moveItem(p, orig); err != nil {
			logf("could not put back %s: %v", orig, err)
			return
		}
		os.Remove(marker)
	case lexists(orig):
		os.RemoveAll(p)
	default:
		moveItem(p, orig)
		os.Remove(marker)
	}
	logf("cleaned up an unfinished update: %s", p)
}

// keepListName is the file a mod ships to name files the player's copy keeps.
const keepListName = "ace8-keep.txt"

// readKeepList reads ace8-keep.txt: one path per line, relative to the mod's
// folder; "#" starts a comment; a folder keeps everything in it; * and ? work
// within one path part. Case doesn't matter.
func readKeepList(p string) []string {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		if i := strings.Index(l, "#"); i >= 0 {
			l = l[:i]
		}
		l = strings.Trim(strings.TrimSpace(strings.ReplaceAll(l, `\`, "/")), "/")
		if l != "" && !strings.Contains(l, "..") {
			out = append(out, strings.ToLower(l))
		}
	}
	return out
}

// keepMatch: rel (a file path in the mod, with /) is covered by the keep list.
func keepMatch(rel string, keep []string) bool {
	rel = strings.ToLower(rel)
	parts := strings.Split(rel, "/")
	for _, k := range keep {
		kn := len(strings.Split(k, "/"))
		if kn > len(parts) {
			continue
		}
		// the pattern names this file, or a folder it is in
		if ok, _ := path.Match(k, strings.Join(parts[:kn], "/")); ok {
			return true
		}
	}
	return false
}

// carryOver copies into newDir (the staged new copy) the files of oldDir (the
// installed copy) that the new copy doesn't have, and the files the keep list
// says the player's copy wins for.
func carryOver(oldDir, newDir string) (carried, kept int, err error) {
	keep := readKeepList(join(newDir, keepListName))
	if keep == nil {
		keep = readKeepList(join(oldDir, keepListName))
	}
	var firstErr error
	filepath.Walk(oldDir, func(p string, info os.FileInfo, werr error) error {
		if werr != nil || info.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(oldDir, p)
		if rerr != nil {
			return nil
		}
		slash := filepath.ToSlash(rel)
		if strings.EqualFold(slash, keepListName) {
			return nil // the download's own list wins
		}
		dst := join(newDir, rel)
		switch {
		case !exists(dst):
			if err := copyFile(p, dst, info.Mode()); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s: %v", slash, err)
				}
				return nil
			}
			if !strings.EqualFold(slash, "enabled.txt") && !strings.EqualFold(slash, "enabled.off") {
				carried++ // the on/off switch is the launcher's, not the player's files
			}
		case keepMatch(slash, keep):
			if same, _ := sameContent(p, dst); same {
				return nil
			}
			if err := copyFile(p, dst, info.Mode()); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s: %v", slash, err)
				}
				return nil
			}
			kept++
		}
		return nil
	})
	return carried, kept, firstErr
}

func sameContent(a, b string) (bool, error) {
	sa, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if sa.Size() != sb.Size() {
		return false, nil
	}
	fa, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer fb.Close()
	ba, bb := make([]byte, 64<<10), make([]byte, 64<<10)
	for {
		na, ea := io.ReadFull(fa, ba)
		nb, eb := io.ReadFull(fb, bb)
		if na != nb || !bytes.Equal(ba[:na], bb[:nb]) {
			return false, nil
		}
		end := func(e error) bool { return e == io.EOF || e == io.ErrUnexpectedEOF }
		if end(ea) && end(eb) {
			return true, nil
		}
		if ea != nil && !end(ea) {
			return false, ea
		}
		if eb != nil && !end(eb) {
			return false, eb
		}
		if end(ea) != end(eb) {
			return false, nil
		}
	}
}

// UE4SS's own files in ue4ss\Mods\shared: added when missing, never overwritten.
func isStockShared(rel string) bool {
	first := strings.ToLower(strings.SplitN(rel, "/", 2)[0])
	switch first {
	case "types.lua", "uehelpers", "jsbprofiler":
		return true
	}
	return false
}

type sharedResult struct {
	Added, Replaced, Same int
	KeptStock             []string
	Backup                string // the overwritten files, while the update runs
}

// commitShared puts the staged files of the shared folder in place, file by file.
func (g Game) commitShared(u *unit, tmp, source string) (sharedResult, error) {
	var r sharedResult
	rels := make([]string, 0, len(u.files))
	for rel := range u.files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	skip := map[string]bool{}
	var replace []string
	for _, rel := range rels {
		dst := join(u.target, filepath.FromSlash(rel))
		if !exists(dst) {
			continue
		}
		if same, _ := sameContent(join(tmp, filepath.FromSlash(rel)), dst); same {
			r.Same++
			skip[rel] = true
			continue
		}
		if isStockShared(rel) {
			r.KeptStock = append(r.KeptStock, rel)
			skip[rel] = true
			continue
		}
		replace = append(replace, rel)
	}
	// the files about to be overwritten are copied aside, only to put them back
	// if the update fails part way; they are deleted afterwards
	if len(replace) > 0 {
		dir := tmp + "-old"
		os.RemoveAll(dir)
		for _, rel := range replace {
			if err := copyFile(join(u.target, filepath.FromSlash(rel)), join(dir, "files", filepath.FromSlash(rel)), 0o644); err != nil {
				os.RemoveAll(dir)
				return r, fmt.Errorf("could not read shared\\%s: %v", rel, err)
			}
		}
		r.Backup = dir
	}
	// put the new files in place; if one fails, undo
	var added []string
	undo := func() {
		for _, a := range added {
			os.Remove(a)
		}
		for _, rel := range replace {
			copyFile(join(r.Backup, "files", filepath.FromSlash(rel)), join(u.target, filepath.FromSlash(rel)), 0o644)
		}
		if r.Backup != "" {
			os.RemoveAll(r.Backup)
		}
	}
	for _, rel := range rels {
		if skip[rel] {
			continue
		}
		dst := join(u.target, filepath.FromSlash(rel))
		existed := exists(dst)
		if err := copyFile(join(tmp, filepath.FromSlash(rel)), dst, 0o644); err != nil {
			undo()
			return sharedResult{}, err
		}
		if existed {
			r.Replaced++
		} else {
			r.Added++
			added = append(added, dst)
		}
	}
	if r.Backup != "" {
		os.RemoveAll(r.Backup)
		r.Backup = ""
	}
	logf("shared files from %s: %d added, %d replaced, %d already the same, UE4SS's own kept: %v", source, r.Added, r.Replaced, r.Same, r.KeptStock)
	return r, nil
}

// restoreShared puts back the shared files an update replaced.
func (g Game) restoreShared(dir string, ri removedInfo) error {
	to := g.relocate(ri.From)
	for _, rel := range ri.Files {
		if !isFile(join(dir, "files", filepath.FromSlash(rel))) {
			return errors.New("the backup of shared\\" + rel + " is missing")
		}
	}
	for _, rel := range ri.Files {
		if err := copyFile(join(dir, "files", filepath.FromSlash(rel)), join(to, filepath.FromSlash(rel)), 0o644); err != nil {
			return err
		}
	}
	return os.RemoveAll(dir)
}

// plural: "1 file", "3 files".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
