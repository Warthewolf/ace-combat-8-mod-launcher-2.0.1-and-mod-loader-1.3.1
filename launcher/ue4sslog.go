package main

// Reads ue4ss\UE4SS.log for which script mods UE4SS started. The Mod Loader's
// log only covers pak mods; script mods (Lua and C++) are started by UE4SS.

import (
	"os"
	"regexp"
	"strings"
	"time"
)

type ScriptRun struct {
	When     time.Time       `json:"when"`
	Started  map[string]bool `json:"started"` // mod name, lower case
	Problems []string        `json:"problems,omitempty"`
}

var (
	// UE4SS 3.x: "Mod 'Name' has enabled.txt, starting mod."
	reScriptStartedTxt = regexp.MustCompile(`Mod '(.+?)' has enabled\.txt, starting mod`)
	// started from mods.txt: "Starting Lua mod 'Name'" / "Starting C++ mod 'Name'"
	reScriptStarted = regexp.MustCompile(`Starting (?:Lua|C\+\+) mod '(.+?)'`)
	reScriptProblem = regexp.MustCompile(`(?i)(\[Lua\].*error|error.*\[Lua\]|failed to load (?:dll|mod)|could not load|lua_pcall)`)
)

// readScriptRun returns nil when UE4SS's log can't be read or names no started
// mods (then the window says nothing rather than guess).
func readScriptRun(ue4ssRoot string) *ScriptRun {
	p := join(ue4ssRoot, "UE4SS.log")
	st, err := os.Stat(p)
	if err != nil || st.Size() > 64<<20 {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	sr := parseScriptRun(decodeText(b))
	if sr == nil {
		return nil
	}
	sr.When = st.ModTime()
	return sr
}

func parseScriptRun(text string) *ScriptRun {
	sr := &ScriptRun{Started: map[string]bool{}}
	seen := map[string]bool{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		if m := reScriptStartedTxt.FindStringSubmatch(line); m != nil {
			sr.Started[strings.ToLower(m[1])] = true
			continue
		}
		if m := reScriptStarted.FindStringSubmatch(line); m != nil {
			sr.Started[strings.ToLower(m[1])] = true
			continue
		}
		if reScriptProblem.MatchString(line) && len(sr.Problems) < 8 {
			// drop the "[time]" prefix UE4SS puts on each line
			msg := line
			if i := strings.Index(msg, "] "); strings.HasPrefix(msg, "[") && i > 0 {
				msg = msg[i+2:]
			}
			msg = strings.TrimSpace(msg)
			if msg != "" && !seen[msg] {
				seen[msg] = true
				sr.Problems = append(sr.Problems, msg)
			}
		}
	}
	if len(sr.Started) == 0 {
		return nil
	}
	return sr
}
