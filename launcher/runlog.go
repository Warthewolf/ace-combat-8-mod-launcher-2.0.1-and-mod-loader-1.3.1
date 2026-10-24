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
	When     time.Time          `json:"when"`
	Version  string             `json:"version"`
	State    string             `json:"state"` // ok, problems, waiting, unsupported, otherloader, nothing
	Summary  string             `json:"summary"`
	Mounted  int                `json:"mounted"`
	Total    int                `json:"total"`
	NewFiles string             `json:"newFiles,omitempty"`
	Problems []string           `json:"problems,omitempty"`
	Mods     map[string]*ModRun `json:"-"`
}

var (
	reVersion  = regexp.MustCompile(`^AC8 mod loader (\d+\.\d+\.\d+)`)
	reMounting = regexp.MustCompile(`^mounting (?:Legacy Pak )?\[(.*)\] order=-?\d+ path=(.*)$`)
	reResult   = regexp.MustCompile(`^\s+result code=(-?\d+) message=(.*)$`)
	// 1.3: "  Legacy result AL=1 pak-in-table=1 (...)" for a *_P.pak
	rePakResult = regexp.MustCompile(`^\s+Legacy result AL=(\d+) pak-in-table=(\d+)`)
	// 1.1: "done: 4 of 4 containers mounted from 3 mod(s)"
	// 1.3: "done: 3 of 7 attempted mount requests returned success (8 planned, 2 mod(s))"
	reDone    = regexp.MustCompile(`^done: (\d+) of (\d+) `)
	reSkipped = regexp.MustCompile(`^\s+skipped (.*?): (.*)$`)
	reHooked  = regexp.MustCompile(`already hooked by: (.*)$`)
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

func parseRunLog(text string) *RunInfo {
	ri := &RunInfo{Mods: map[string]*ModRun{}, State: "waiting"}
	cur := ""
	sawDone, sawHook := false, false
	pakSeen := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := reVersion.FindStringSubmatch(line); m != nil {
			// UE4SS restarted the loader in the same session: keep reading, the later part wins.
			ri.Version = m[1]
			continue
		}
		switch {
		case strings.Contains(line, "patterns not found exactly once"):
			ri.State = "unsupported"
			ri.Summary = "This game version is not supported by the Mod Loader, so no mods were loaded. The Mod Loader needs an update after a game update."
		case reHooked.MatchString(line):
			ri.State = "otherloader"
			ri.Summary = "Another copy of a mod loader is installed (" + reHooked.FindStringSubmatch(line)[1] + "). Only one can work: remove the other copy."
		case strings.HasPrefix(line, "hook installed"):
			sawHook = true
		case strings.HasPrefix(line, "nothing to mount"):
			ri.State = "nothing"
			ri.Summary = "No mods were switched on, so nothing was loaded."
			sawDone = true
		}
		if m := reMounting.FindStringSubmatch(line); m != nil {
			cur = strings.ToLower(m[1])
			if ri.Mods[cur] == nil {
				ri.Mods[cur] = &ModRun{}
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
				r.Messages = append(r.Messages, "The game refused a file: "+msg)
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
					r.Messages = append(r.Messages, "The game took the .pak but reported a problem with it (AL=0).")
				}
			} else {
				r.Failed++
				r.Messages = append(r.Messages, "The game did not take a .pak file.")
			}
			pakSeen = true
			cur = ""
			continue
		}
		if strings.HasPrefix(line, "Legacy Pak active mounting unavailable") {
			ri.Problems = append(ri.Problems, "The Mod Loader could not set up .pak loading for this game version, so _P.pak mods were not loaded.")
			continue
		}
		if m := reSkipped.FindStringSubmatch(line); m != nil {
			if strings.Contains(m[2], "already present in the native mount table") {
				continue // the game loaded that .pak itself
			}
			ri.Problems = append(ri.Problems, "Skipped "+strings.TrimPrefix(shortPath(m[1]), "Pak ")+": "+m[2])
			continue
		}
		if m := reDone.FindStringSubmatch(line); m != nil {
			ri.Mounted, _ = strconv.Atoi(m[1])
			ri.Total, _ = strconv.Atoi(m[2])
			if pakSeen {
				// 1.3 counts a .pak as a success only when AL=1; count what the game took
				ri.Mounted, ri.Total = 0, 0
				for _, r := range ri.Mods {
					ri.Mounted += r.Loaded
					ri.Total += r.Loaded + r.Failed
				}
			}
			sawDone = true
			continue
		}
		if strings.HasPrefix(line, "new files: ") {
			ri.NewFiles = strings.TrimPrefix(line, "new files: ")
		}
	}
	if ri.State == "waiting" && sawDone {
		ri.State = "ok"
		if ri.Mounted < ri.Total {
			ri.State = "problems"
		}
		ri.Summary = strconv.Itoa(ri.Mounted) + " of " + strconv.Itoa(ri.Total) + " mod files loaded."
	}
	if ri.State == "waiting" {
		if sawHook {
			ri.Summary = "The Mod Loader was ready, but the game had not loaded its files yet (still starting, or closed early)."
		} else {
			ri.Summary = "The Mod Loader started but did not finish. Look at the log for details."
		}
	}
	for _, r := range ri.Mods {
		if r.Failed > 0 && ri.State == "ok" {
			ri.State = "problems"
		}
	}
	return ri
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
