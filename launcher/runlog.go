package main

// Reads AC8IoStoreLoader.log, which the Mod Loader writes each time the game
// starts, into a short summary and a result per mod.

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type RunInfo struct {
	When     time.Time `json:"when"`
	Version  string    `json:"version"`
	State    string    `json:"state"` // ok, problems, waiting, unsupported, otherloader, nothing
	Summary  string    `json:"summary"`
	Mounted  int       `json:"mounted"`
	Total    int       `json:"total"`
	NewFiles string    `json:"newFiles,omitempty"`
	// Problems says, in words a player understands, each mod file that was
	// switched on but not loaded, and why.
	Problems []string           `json:"problems,omitempty"`
	Mods     map[string]*ModRun `json:"-"`
	// notLoaded: by mod key (lower case), files the Mod Loader skipped for a
	// reason the launcher can't see coming; addModNotes shows them on the mod.
	notLoaded map[string][]runNote
}

type runNote struct{ file, text string }

var (
	reVersion  = regexp.MustCompile(`^AC8 mod loader (\d+\.\d+\.\d+)`)
	reMounting = regexp.MustCompile(`^mounting (?:Legacy Pak )?\[(.*)\] order=-?\d+ path=(.*)$`)
	reResult   = regexp.MustCompile(`^\s+result code=(-?\d+) message=(.*)$`)
	// 1.3: "  Legacy result AL=1 pak-in-table=1 (...)" for a *_P.pak
	rePakResult = regexp.MustCompile(`^\s+Legacy result AL=(\d+) pak-in-table=(\d+)`)
	// 1.1: "done: 4 of 4 containers mounted from 3 mod(s)"
	// 1.3: "done: 3 of 7 attempted mount requests returned success (8 planned, 2 mod(s))"
	reDone = regexp.MustCompile(`^done: (\d+) of (\d+) `)
	// "  skipped <path>: <why>" while the plan is made, "  skipped Pak <path>: <why>" while mounting
	reSkipped = regexp.MustCompile(`^\s+skipped (?:Pak )?(.*?): (.*)$`)
	reHooked  = regexp.MustCompile(`already hooked by: (.*)$`)
	// another program hooked only the game's .pak reader (a second loader or a signature bypass)
	reOtherPakHook = regexp.MustCompile(`Pak constructor already hooked by another loader`)
	reModRoot      = regexp.MustCompile(`^mod root=(.+)$`)
	reTildeRoot    = regexp.MustCompile(`^~mods folder=(.+)$`)
	reUcasName     = regexp.MustCompile(`\(([^()]+\.ucas)\)`)
	// 1.3: the Mod Loader's .pak hook saw the game open a *_P.pak:
	// "custom pak: signedMode=1 -> 0 path=../../../Game/Content/Paks/~mods/Font_P.pak"
	reCustomPak = regexp.MustCompile(`^custom pak: signedMode=-?\d+ -> 0 path=(.*)$`)
)

func readRunInfo(loaderDir string) *RunInfo {
	p := join(loaderDir, "AC8IoStoreLoader.log")
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	ri := parseRunLog(decodeText(b))
	ri.When = st.ModTime()
	return ri
}

// logPath puts a path from the log in one form for comparing: backslashes, lower case.
func logPath(p string) string {
	return strings.ToLower(strings.TrimSpace(strings.ReplaceAll(p, "/", `\`)))
}

// runSkip is a container the Mod Loader skipped. A later "mounting" line for
// the same path (the deferred .pak pass) takes it back, and a later skip for
// another reason replaces it: the last pass decides.
type runSkip struct {
	path string // logPath form
	orig string // as the log wrote it
	why  string
	gone bool
}

func parseRunLog(text string) *RunInfo {
	ri := &RunInfo{Mods: map[string]*ModRun{}, State: "waiting", notLoaded: map[string][]runNote{}}
	cur := ""
	sawDone, sawHook, nothing := false, false, false
	pakUnavailable := false
	// pakHook: the Mod Loader's .pak hook was in place; startedLate: only after
	// the game had opened its .pak files (~mods too). gameOpened: the file
	// names of the ~mods *_P.pak files the hook saw the game open; mounted:
	// the file names the Mod Loader mounted itself.
	pakHook, startedLate := false, false
	gameOpened, mounted := map[string]bool{}, map[string]bool{}
	modRoot, tildeRoot := "", ""
	var skips []*runSkip
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := reVersion.FindStringSubmatch(line); m != nil {
			// UE4SS restarted the loader in the same session: keep reading, the later part wins.
			ri.Version = m[1]
			continue
		}
		if m := reModRoot.FindStringSubmatch(line); m != nil {
			modRoot = logPath(m[1])
			continue
		}
		if m := reTildeRoot.FindStringSubmatch(line); m != nil {
			if m[1] != "(none)" {
				tildeRoot = logPath(m[1])
			}
			continue
		}
		switch {
		case strings.Contains(line, "patterns not found exactly once"):
			ri.State = "unsupported"
			ri.Summary = "No mods were loaded: the Mod Loader doesn't work with this game version yet. After a game update, the Mod Loader needs an update too: install the new one when it is out."
		case reHooked.MatchString(line):
			ri.State = "otherloader"
			ri.Summary = "The Mod Loader did nothing, because another mod loader is installed (" + reHooked.FindStringSubmatch(line)[1] + "). Only one can work: remove the other one."
		case reOtherPakHook.MatchString(line):
			ri.State = "otherloader"
			ri.Summary = "The Mod Loader did nothing, because another mod loader or a .pak signature bypass is installed. Only one can work: remove the other one. Look for other mod loader folders in ue4ss\\Mods, and for .dll files next to AceCombat8.exe other than dwmapi.dll."
		case strings.HasPrefix(line, "hook installed"):
			sawHook = true
		case strings.HasPrefix(line, "Pak hook installed"):
			pakHook = true
		case strings.HasPrefix(line, "the loader started after the game mounted"):
			startedLate = true
		case strings.HasPrefix(line, "nothing to mount"):
			nothing = true
			sawDone = true
		}
		if m := reCustomPak.FindStringSubmatch(line); m != nil {
			if p := logPath(m[1]); strings.HasPrefix(p, `~mods\`) || strings.Contains(p, `\~mods\`) {
				gameOpened[p[strings.LastIndex(p, `\`)+1:]] = true
			}
			continue
		}
		if m := reMounting.FindStringSubmatch(line); m != nil {
			cur = strings.ToLower(m[1])
			if ri.Mods[cur] == nil {
				ri.Mods[cur] = &ModRun{}
			}
			p := logPath(m[2])
			mounted[p[strings.LastIndex(p, `\`)+1:]] = true
			for _, s := range skips {
				if s.path == p {
					s.gone = true // the deferred .pak pass mounted it after all
				}
			}
			continue
		}
		if m := reResult.FindStringSubmatch(line); m != nil && cur != "" {
			code, _ := strconv.Atoi(m[1])
			r := ri.Mods[cur]
			if code == 0 {
				r.Loaded++
			} else {
				r.Failed++
				msg := strings.TrimSpace(m[2])
				if msg == "" {
					msg = "error " + m[1]
				}
				r.Messages = append(r.Messages, "The game couldn't read this mod. It may be damaged or made for another game version: download it again, or ask its author. ("+msg+")")
			}
			cur = ""
			continue
		}
		if m := rePakResult.FindStringSubmatch(line); m != nil && cur != "" {
			// The game has the .pak once it is in its table. Mod Loader 1.3 notes that a
			// .pak can be in the table while the game still reports a problem (AL=0).
			r := ri.Mods[cur]
			if m[2] == "1" {
				r.Loaded++
				if m[1] != "1" {
					r.Messages = append(r.Messages, "The game loaded this .pak but reported a problem with it, so part of the mod may not work. If something is missing in game, download it again or ask its author. (AL=0)")
				}
			} else {
				r.Failed++
				r.Messages = append(r.Messages, "The game couldn't load this mod's .pak file. It may be damaged or made for another game version: download it again, or ask its author. (AL="+m[1]+", not in the game's table)")
			}
			cur = ""
			continue
		}
		if strings.HasPrefix(line, "Legacy Pak active mounting unavailable") {
			pakUnavailable = true
			continue
		}
		if m := reSkipped.FindStringSubmatch(line); m != nil {
			p := logPath(m[1])
			dup := false
			for _, s := range skips {
				if s.path != p || s.gone {
					continue
				}
				if s.why == m[2] {
					dup = true // the deferred .pak pass checks the plan again
				} else {
					s.gone = true // and its answer is the one that counts
				}
			}
			if !dup {
				skips = append(skips, &runSkip{path: p, orig: strings.TrimSpace(m[1]), why: m[2]})
			}
			continue
		}
		if reDone.MatchString(line) {
			sawDone = true
			continue
		}
		if strings.HasPrefix(line, "new files: ") {
			ri.NewFiles = strings.TrimPrefix(line, "new files: ")
		}
	}

	// What the skipped containers mean for the player.
	runFor := func(key string) *ModRun {
		if ri.Mods[key] == nil {
			ri.Mods[key] = &ModRun{}
		}
		return ri.Mods[key]
	}
	notLoaded, unavailable := 0, 0
	for _, s := range skips {
		if s.gone {
			continue
		}
		key, tilde := skipKey(s.path, modRoot, tildeRoot)
		where := skipWhere(s.orig, modRoot, tildeRoot)
		file := where
		if i := strings.LastIndex(file, `\`); i >= 0 {
			file = file[i+1:]
		}
		// A *_P.pak in ~mods that the game opened itself, through the Mod
		// Loader's .pak hook, is loaded whatever the Mod Loader then decided
		// about it: it can't mount .pak files on this game version, it couldn't
		// check the game's table yet, or a newer copy elsewhere has its name.
		// Unless the Mod Loader mounted a file of that name: then the game's
		// table didn't have it.
		name := s.path[strings.LastIndex(s.path, `\`)+1:]
		gameLoaded := tilde && isPakName(name) && (gameOpened[name] || pakHook && !startedLate) && !mounted[name]
		switch {
		case strings.Contains(s.why, "already present in the native mount table") || strings.Contains(s.why, "already mounted a container with this name"),
			gameLoaded && (strings.Contains(s.why, "unavailable") || strings.HasPrefix(s.why, "same name as")):
			if tilde {
				// .pak files in ~mods: the game mounts them itself, before the Mod Loader runs
				r := runFor(key)
				r.Loaded++
				r.Messages = append(r.Messages, "Loaded by the game itself, because it is a .pak in the ~mods folder.")
				continue
			}
			notLoaded++
			msg := "Not loaded at the last game start: the game already had another file named " + file + " (often the same mod in ~mods too), and only one can load. Keep only one copy."
			ri.notLoaded[key] = append(ri.notLoaded[key], runNote{file, msg})
			ri.Problems = append(ri.Problems, where+" was not loaded: the game already had another file with this name. Keep only one copy.")
		case strings.Contains(s.why, "unavailable"):
			unavailable++
			r := runFor(key)
			r.Failed++
			if pakUnavailable {
				r.Messages = append(r.Messages, "Not loaded: the Mod Loader can't load .pak mods on this game version yet. Install the new Mod Loader when it is out. (.pak loading unavailable)")
			} else {
				r.Messages = append(r.Messages, "Not loaded: the game wasn't ready for .pak mods yet. Start the game again. (.pak loading deferred)")
			}
		case strings.Contains(s.why, ".ucas file is missing"):
			notLoaded++
			ucas := file
			if m := reUcasName.FindStringSubmatch(s.why); m != nil {
				ucas = m[1]
			}
			ri.Problems = append(ri.Problems, where+" was not loaded: part of the mod is missing ("+ucas+"). Download it again, or ask its author for the full files.")
		case strings.HasPrefix(s.why, "same name as"):
			notLoaded++
			ri.Problems = append(ri.Problems, where+" was not loaded: another copy with the same name was newer, and only one can load. Remove the copy you don't want.")
		default:
			notLoaded++
			msg := "Not loaded at the last game start: the Mod Loader skipped " + file + ". (" + s.why + ")"
			ri.notLoaded[key] = append(ri.notLoaded[key], runNote{file, msg})
			ri.Problems = append(ri.Problems, where+" was not loaded. ("+s.why+")")
		}
	}
	if unavailable > 0 {
		what := countOf(unavailable, ".pak mod file was", ".pak mod files were")
		if pakUnavailable {
			ri.Problems = append([]string{what + " not loaded: the Mod Loader can't load .pak mods on this game version yet. Install the new Mod Loader when it is out. (.pak loading unavailable)"}, ri.Problems...)
		} else {
			ri.Problems = append([]string{what + " not loaded: the game wasn't ready for them yet. Start the game again; if this keeps happening, the Mod Loader may need an update. (.pak loading deferred)"}, ri.Problems...)
		}
	}

	if sawDone && ri.State == "waiting" {
		// Count every file the Mod Loader had switched on: what the game took
		// (also the ~mods .pak files it loaded itself), what it refused, and what
		// was skipped. The log's own "done:" count leaves the skipped ones out.
		ri.Mounted, ri.Total = 0, notLoaded
		for _, r := range ri.Mods {
			ri.Mounted += r.Loaded
			ri.Total += r.Loaded + r.Failed
		}
		if nothing && ri.Total == 0 {
			ri.State = "nothing"
			ri.Summary = "No pak mods were switched on, so the Mod Loader had nothing to load."
		} else {
			ri.State = "ok"
			if ri.Mounted < ri.Total {
				ri.State = "problems"
			}
			ri.Summary = strconv.Itoa(ri.Mounted) + " of " + strconv.Itoa(ri.Total) + " mod files loaded."
			switch {
			case unavailable > 0 && pakUnavailable:
				ri.Summary += " The Mod Loader can't load .pak mods on this game version yet, so " + countOf(unavailable, "file was", "files were") + " left out. Install the new Mod Loader when it is out."
			case unavailable > 0:
				ri.Summary += " " + countOf(unavailable, ".pak file was", ".pak files were") + " left out because the game wasn't ready for them. Start the game again."
			}
		}
	}
	if ri.State == "waiting" {
		if sawHook {
			ri.Summary = "The Mod Loader was ready, but the game had not loaded its files yet (still starting, or closed early)."
		} else {
			ri.Summary = "The Mod Loader started but didn't finish, so the mods may not have loaded. Start the game again. If it happens again, copy the support report in Settings and ask for help."
		}
	}
	for _, r := range ri.Mods {
		if r.Failed > 0 && ri.State == "ok" {
			ri.State = "problems"
		}
	}
	return ri
}

// skipKey finds the mod a container path from the log belongs to: the folder
// (or loose file) right under the Mod Loader's folder or ~mods, the same name
// the log uses in "mounting [Name]". tilde reports a container in ~mods.
func skipKey(p, modRoot, tildeRoot string) (key string, tilde bool) {
	first := func(rel string) string {
		rel = strings.TrimLeft(rel, `\`)
		if i := strings.Index(rel, `\`); i >= 0 {
			return rel[:i]
		}
		return rel
	}
	if tildeRoot != "" && strings.HasPrefix(p, strings.TrimRight(tildeRoot, `\`)+`\`) {
		return `~mods\` + first(p[len(strings.TrimRight(tildeRoot, `\`)):]), true
	}
	if modRoot != "" && strings.HasPrefix(p, strings.TrimRight(modRoot, `\`)+`\`) {
		return first(p[len(strings.TrimRight(modRoot, `\`)):]), false
	}
	if i := strings.Index(p, `\paks\~mods\`); i >= 0 {
		return `~mods\` + first(p[i+len(`\paks\~mods\`):]), true
	}
	if strings.HasPrefix(p, `~mods\`) {
		return `~mods\` + first(p[len(`~mods\`):]), true
	}
	if i := strings.Index(p, `\iostoreloadermod\`); i >= 0 {
		return first(p[i+len(`\iostoreloadermod\`):]), false
	}
	return first(p), false
}

// skipWhere is a container path from the log as the window shows it: from the
// mod's folder on, like "Font\Font_P.pak" or "~mods\Font_P.pak".
func skipWhere(orig, modRoot, tildeRoot string) string {
	p := strings.ReplaceAll(orig, "/", `\`)
	lp := strings.ToLower(p)
	for _, r := range []struct{ root, prefix string }{{tildeRoot, `~mods\`}, {modRoot, ""}} {
		root := strings.TrimRight(r.root, `\`) + `\`
		if r.root != "" && len(lp) == len(p) && strings.HasPrefix(lp, root) {
			return r.prefix + p[len(root):]
		}
	}
	return shortPath(p)
}

// countOf: "1 file was", "3 files were".
func countOf(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

func shortPath(p string) string {
	p = strings.ReplaceAll(p, "/", `\`)
	if i := strings.Index(strings.ToLower(p), `\iostoreloadermod\`); i >= 0 {
		return p[i+len(`\iostoreloadermod\`):]
	}
	if i := strings.Index(strings.ToLower(p), `\paks\`); i >= 0 {
		return p[i+len(`\paks\`):]
	}
	return p
}
