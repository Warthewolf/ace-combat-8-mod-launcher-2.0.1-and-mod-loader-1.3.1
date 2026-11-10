package main

// Reads ue4ss\UE4SS.log for which script mods UE4SS started. The Mod Loader's
// log only covers pak mods; script mods (Lua and C++) are started by UE4SS.

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type ScriptRun struct {
	When time.Time `json:"when"`
	// Started holds, in lower case, the name UE4SS gives each mod it started and
	// the name of each folder that mod is in ("ac8" and "ac8.stonehenge").
	Started  map[string]bool `json:"started"`
	Problems []string        `json:"problems,omitempty"`
}

var (
	// UE4SS 3.x: "Mod 'Name' has enabled.txt, starting mod."
	reScriptStartedTxt = regexp.MustCompile(`Mod '(.+?)' has enabled\.txt, starting mod`)
	// started from mods.txt: "Starting Lua mod 'Name'" / "Starting C++ mod 'Name'".
	// The name runs to the last quote: it can hold one itself ("Mobius1's_HUD").
	reScriptStarted = regexp.MustCompile(`Starting (?:Lua|C\+\+) mod '(.+)'\s*$`)
	// Script errors. UE4SS 3.0.1 as shipped (e3ba1016) writes "Error loading
	// script: ...main.lua:3: ..." for a script that doesn't compile, "Error
	// executing script" and "Failed to execute main script" when it fails while
	// starting; the v3.0.1 tag writes "[Lua::execute_file] luaL_loadfile returned ...".
	reScriptProblem     = regexp.MustCompile(`(?i)(\[Lua\].*error|error.*\[Lua\]|failed to load (?:dll|mod)|could not load|lua_pcall|luaL_loadfile|\[Lua::execute_file\]|Error loading script|Error executing script|Failed to execute main script|Main script 'main\.lua' not found|Exception (?:during mod (?:startup|preparation)|in load_and_execute_script)|Failed to find exported mod lifecycle functions)`)
	reScriptMainMissing = regexp.MustCompile(`(?i)Main script 'main\.lua' not found`)
	reScriptDll         = regexp.MustCompile(`(?i)failed to load dll|Failed to find exported mod lifecycle functions`)
	// the mod a problem line is about: a path into ue4ss\Mods\<Name>\ (or any
	// Mods\<Name>\Scripts\), "[Lua] [Name]" or "for mod Name"
	reProblemModPath = regexp.MustCompile(`(?i)[\\/]ue4ss[\\/]Mods[\\/]([^\\/:<>"]+)[\\/]`)
	reProblemModAny  = regexp.MustCompile(`(?i)[\\/]Mods[\\/]([^\\/:<>"]+)[\\/](?:Scripts|dlls)[\\/]`)
	reProblemModLua  = regexp.MustCompile(`\[Lua\]\s*\[([^\]]+)\]`)
	reProblemModFor  = regexp.MustCompile(`for mod ([^,]+?)(?:,|$)`)
	// a full path up to UE4SS's Mods folder, left out of the message to keep it short
	reProblemPath = regexp.MustCompile(`(?i)@?[A-Za-z]:[\\/][^<>"|?*\r\n]*?[\\/]ue4ss[\\/]Mods[\\/]`)
)

// lastScriptRun is what parseScriptRun gave for the UE4SS.log read last, by
// its path, size and date. The log stays the same from one game start to the
// next, and a big one takes a moment to read, so the state loads in between
// don't read it again. Only a log last written a few seconds before is kept:
// a file's date can be coarse, and a log written again within the same tick
// must still be read again.
var lastScriptRun struct {
	sync.Mutex
	path  string
	size  int64
	mtime time.Time
	sr    *ScriptRun
}

// readScriptRun returns nil when UE4SS's log can't be read or names no started
// mods (then the window says nothing rather than guess).
func readScriptRun(ue4ssRoot string) *ScriptRun {
	p := join(ue4ssRoot, "UE4SS.log")
	st, err := os.Stat(p)
	if err != nil || st.Size() > 64<<20 {
		return nil
	}
	c := &lastScriptRun
	c.Lock()
	sr, known := c.sr, c.path == p && c.size == st.Size() && c.mtime.Equal(st.ModTime())
	c.Unlock()
	if !known {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		sr = parseScriptRun(decodeText(b))
		if time.Since(st.ModTime()) > 3*time.Second {
			c.Lock()
			c.path, c.size, c.mtime, c.sr = p, st.Size(), st.ModTime(), sr
			c.Unlock()
		}
	}
	if sr == nil {
		return nil
	}
	sr = sr.clone() // addFolders changes it
	sr.When = st.ModTime()
	sr.addFolders(listScriptMods(ue4ssRoot))
	return sr
}

func (sr *ScriptRun) clone() *ScriptRun {
	c := &ScriptRun{When: sr.When, Started: make(map[string]bool, len(sr.Started))}
	for k, v := range sr.Started {
		c.Started[k] = v
	}
	if sr.Problems != nil {
		c.Problems = append([]string{}, sr.Problems...)
	}
	return c
}

// addFolders adds each script mod folder UE4SS started under its own folder
// name, the name the window looks up. It also says when two folders of the
// same kind (Lua, C++) get the same name in UE4SS: then only one of them runs.
func (sr *ScriptRun) addFolders(mods []*ScriptMod) {
	type group struct {
		folders  []string
		lua, cpp int
	}
	byName := map[string]*group{}
	var names []string
	for _, m := range mods {
		if sr.Started[strings.ToLower(m.UE4SSName)] {
			sr.Started[strings.ToLower(m.Name)] = true
		}
		g := byName[m.UE4SSName]
		if g == nil {
			g = &group{}
			byName[m.UE4SSName] = g
			names = append(names, m.UE4SSName)
		}
		g.folders = append(g.folders, m.Name)
		if strings.Contains(m.Kind, "Lua") {
			g.lua++
		}
		if strings.Contains(m.Kind, "C++") {
			g.cpp++
		}
	}
	sort.Strings(names)
	for _, n := range names {
		if g := byName[n]; g.lua > 1 || g.cpp > 1 {
			sr.Problems = append(sr.Problems, "Only one of "+strings.Join(g.folders, " and ")+" can run: UE4SS leaves out what comes after the last dot in a folder's name, so it sees each of them as "+n+". Rename the folders so their names differ before the dot.")
		}
	}
}

func parseScriptRun(text string) *ScriptRun {
	sr := &ScriptRun{Started: map[string]bool{}}
	seen := map[string]bool{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		// The log can be tens of MB of a mod's own prints: a cheap look for
		// the words each pattern needs comes first, and most lines stop there.
		if strings.Contains(line, "' has enabled.txt, starting mod") {
			if m := reScriptStartedTxt.FindStringSubmatch(line); m != nil {
				sr.Started[strings.ToLower(m[1])] = true
				continue
			}
		}
		if strings.Contains(line, "Starting ") {
			if m := reScriptStarted.FindStringSubmatch(line); m != nil {
				sr.Started[strings.ToLower(m[1])] = true
				continue
			}
		}
		if len(sr.Problems) < 8 && mayBeScriptProblem(line) && reScriptProblem.MatchString(line) {
			// drop the "[time]" prefix UE4SS puts on each line
			msg := line
			if i := strings.Index(msg, "] "); strings.HasPrefix(msg, "[") && i > 0 {
				msg = msg[i+2:]
			}
			msg = strings.TrimSpace(msg)
			if msg == "" {
				continue
			}
			mod := problemMod(msg)
			key := strings.ToLower(mod)
			if mod == "" {
				key = "\x00" + msg
			}
			if seen[key] {
				continue // one line per mod: the first says the most
			}
			seen[key] = true
			sr.Problems = append(sr.Problems, scriptProblemText(mod, msg))
		}
	}
	if len(sr.Started) == 0 {
		return nil
	}
	return sr
}

// mayBeScriptProblem is false for a line reScriptProblem can't match: each of
// its patterns holds one of these words, in any case. (None holds a k or an s,
// letters that also match a non-ASCII one, so comparing bytes is enough.)
func mayBeScriptProblem(line string) bool {
	for i := 0; i < len(line); i++ {
		switch line[i] | 0x20 {
		case 'e':
			if hasWordAt(line, i, "error") || hasWordAt(line, i, "exception") || hasWordAt(line, i, "execute_file") {
				return true
			}
		case 'f':
			if hasWordAt(line, i, "fail") {
				return true
			}
		case 'c':
			if hasWordAt(line, i, "could not load") {
				return true
			}
		case 'l':
			if hasWordAt(line, i, "lua_pcall") || hasWordAt(line, i, "lual_loadfile") {
				return true
			}
		case 'n':
			if hasWordAt(line, i, "not found") {
				return true
			}
		}
	}
	return false
}

// hasWordAt: s holds word (lower case ASCII) at i, its letters in any case.
func hasWordAt(s string, i int, word string) bool {
	if len(s)-i < len(word) {
		return false
	}
	for j := 0; j < len(word); j++ {
		c, w := s[i+j], word[j]
		if c != w && !('a' <= w && w <= 'z' && c == w-32) {
			return false
		}
	}
	return true
}

// problemMod finds the script mod a UE4SS error line is about, or "".
func problemMod(msg string) string {
	for _, re := range []*regexp.Regexp{reProblemModPath, reProblemModAny, reProblemModLua, reProblemModFor} {
		if m := re.FindStringSubmatch(msg); m != nil {
			if n := strings.TrimSpace(m[1]); n != "" {
				return n
			}
		}
	}
	return ""
}

// scriptProblemText says what a UE4SS error means and what to do, with UE4SS's
// own words after it.
func scriptProblemText(mod, msg string) string {
	detail := " (" + reProblemPath.ReplaceAllString(msg, "") + ")"
	if mod == "" {
		return "A script mod reported an error, so it may not work. If one doesn't work in game, tell its author." + detail
	}
	switch {
	case reScriptMainMissing.MatchString(msg):
		return mod + " can't start: UE4SS found no Scripts\\main.lua in it (the name must be exactly main.lua). Download it again, or tell its author." + detail
	case reScriptDll.MatchString(msg):
		return mod + " can't start: UE4SS couldn't load its main.dll. Download it again, or tell its author; it may be made for another UE4SS version." + detail
	}
	return mod + " has an error in its script, so it may not work. Download it again, or tell its author." + detail
}
