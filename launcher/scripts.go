package main

// UE4SS script mods (Lua or C++), such as RoGLaser, SkinSlotText or SkyLive.
// UE4SS (3.0.1) names each mod after its folder, without what comes after the
// last dot ("AC8.Stonehenge" runs as "AC8"). It starts a mod when mods.txt says
// "Name : 1" for exactly that name, and also every mod whose folder has
// enabled.txt, even when mods.txt says "Name : 0".

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
)

type ScriptMod struct {
	Name        string     `json:"name"`      // the folder's name
	UE4SSName   string     `json:"ue4ssName"` // the name UE4SS gives it, in its log and in mods.txt
	Path        string     `json:"path"`
	Enabled     bool       `json:"enabled"`
	Kind        string     `json:"kind"`
	Description string     `json:"description,omitempty"`
	Mtime       time.Time  `json:"mtime"` // newest of the folder and its main file
	Added       *time.Time `json:"added,omitempty"`
	Updated     *time.Time `json:"updated,omitempty"`
}

var notScriptMods = []string{"IoStoreLoaderMod", "shared", "removed-mods"}

var reModsTxt = regexp.MustCompile(`^\s*([^:;]+?)\s*:\s*([01])\s*$`)

// ue4ssName is the name UE4SS gives the mod in folder dir: the folder's name
// without its last extension, like C++'s path::stem() ("AC8.Stonehenge" ->
// "AC8"; a name that starts with its only dot stays whole).
func ue4ssName(dir string) string {
	if i := strings.LastIndex(dir, "."); i > 0 {
		return dir[:i]
	}
	return dir
}

// ue4ssModsTxt reads mods.txt the way UE4SS 3.0.1 does: a line with ';' and a
// line of 4 characters or fewer is skipped, every space is removed, the name
// is what comes before the first ':' and matches a mod's name exactly (case
// too), and the mod is on when what comes after the last ':' starts with 1.
// The result is name -> on; a name on several lines is on when any says 1.
func ue4ssModsTxt(modsDir string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(join(modsDir, "mods.txt"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(decodeText(b), "\n") {
		if name, on := modsTxtName(strings.TrimRight(line, "\r")); name != "" {
			out[name] = out[name] || on
		}
	}
	return out
}

// modsTxtState reads mods.txt like ue4ssModsTxt, by name in lower case, for
// callers that compare without case.
func modsTxtState(modsDir string) map[string]bool {
	out := map[string]bool{}
	for name, on := range ue4ssModsTxt(modsDir) {
		k := strings.ToLower(name)
		out[k] = out[k] || on
	}
	return out
}

func listScriptMods(ue4ssRoot string) []*ScriptMod {
	modsDir := join(ue4ssRoot, "Mods")
	ents, _ := os.ReadDir(modsDir)
	txt := ue4ssModsTxt(modsDir)
	var out []*ScriptMod
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		skip := false
		for _, n := range notScriptMods {
			if strings.EqualFold(e.Name(), n) {
				skip = true
			}
		}
		if skip {
			continue
		}
		dir := join(modsDir, e.Name())
		// UE4SS on Windows, and under Wine on Linux, finds these in any case
		luaPath, dllPath := foldPath(dir, "Scripts", "main.lua"), foldPath(dir, "dlls", "main.dll")
		lua, dll := luaPath != "" && isFile(luaPath), dllPath != "" && isFile(dllPath)
		if !lua && !dll {
			continue
		}
		m := &ScriptMod{Name: e.Name(), UE4SSName: ue4ssName(e.Name()), Path: dir}
		switch {
		case lua && dll:
			m.Kind = "Lua + C++"
		case lua:
			m.Kind = "Lua"
		default:
			m.Kind = "C++"
		}
		m.Enabled = isFileFold(dir, "enabled.txt") || txt[m.UE4SSName]
		for _, p := range []string{dir, luaPath, dllPath} {
			if st, err := os.Stat(p); p != "" && err == nil && st.ModTime().After(m.Mtime) {
				m.Mtime = st.ModTime()
			}
		}
		if lua {
			m.Description = luaDescription(luaPath)
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// luaDescription is the first comment line of main.lua, if it reads like a description.
func luaDescription(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 0; sc.Scan() && n < 8; n++ {
		l := strings.TrimSpace(sc.Text())
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "--") && !strings.HasPrefix(l, "--[[") {
			d := strings.TrimSpace(strings.TrimLeft(l, "-"))
			if len(d) > 3 {
				if len(d) > 140 {
					d = d[:137] + "..."
				}
				return d
			}
		}
		if !strings.HasPrefix(l, "--") {
			break
		}
	}
	return ""
}

// setScriptMod switches a script mod on or off, in enabled.txt, mods.txt and mods.json alike.
func setScriptMod(ue4ssRoot, name string, on bool) error {
	modsDir := join(ue4ssRoot, "Mods")
	var mod *ScriptMod
	for _, m := range listScriptMods(ue4ssRoot) {
		if m.Name == name {
			mod = m
		}
	}
	if mod == nil {
		return errors.New("that script mod is no longer there")
	}
	en, off := join(mod.Path, "enabled.txt"), join(mod.Path, "enabled.off")
	// one that is already there in other capitals is used, not doubled
	if p := foldPath(mod.Path, "enabled.txt"); p != "" {
		en = p
	}
	if p := foldPath(mod.Path, "enabled.off"); p != "" {
		off = p
	}
	if on {
		if !isFile(en) {
			if isFile(off) {
				if err := os.Rename(off, en); err != nil {
					return err
				}
			} else if err := os.WriteFile(en, nil, 0o644); err != nil {
				return err
			}
		}
	} else if isFile(en) {
		os.Remove(off)
		if err := os.Rename(en, off); err != nil {
			return err
		}
	}
	// also the lines with the name UE4SS gives it ("AC8 : 1" for AC8.Stonehenge):
	// those are the ones UE4SS goes by
	if err := setModsTxt(modsDir, name, mod.UE4SSName, on, false); err != nil {
		return err
	}
	setModsJSON(modsDir, name, on)
	return nil
}

// setModsTxtLine changes "Name : 0/1" in mods.txt. With add, a missing line is added
// (above the Keybinds line UE4SS asks to keep last).
func setModsTxtLine(modsDir, name string, on, add bool) error {
	return setModsTxt(modsDir, name, "", on, add)
}

// modsTxtName is the name a mods.txt line gives, read the way UE4SS reads it
// (see ue4ssModsTxt), and whether the line says on; "" for a line UE4SS skips.
func modsTxtName(line string) (name string, on bool) {
	if strings.Contains(line, ";") || len(utf16.Encode([]rune(line))) <= 4 {
		return "", false
	}
	line = strings.ReplaceAll(line, " ", "")
	i := strings.Index(line, ":")
	if i < 0 {
		return "", false
	}
	return line[:i], strings.HasPrefix(line[strings.LastIndex(line, ":")+1:], "1")
}

// setModsTxt is setModsTxtLine that also changes every line UE4SS reads as
// exactly ue4ss (when not ""), keeping how the line spells the name.
func setModsTxt(modsDir, name, ue4ss string, on, add bool) error {
	p := join(modsDir, "mods.txt")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil // no mods.txt: enabled.txt alone decides
	}
	v := "0"
	if on {
		v = "1"
	}
	lines := strings.Split(decodeText(b), "\n")
	changed, found := false, false
	for i, raw := range lines {
		l := strings.TrimRight(raw, "\r")
		if m := reModsTxt.FindStringSubmatch(l); m != nil && strings.EqualFold(m[1], name) {
			found = true
			if m[2] != v {
				lines[i] = name + " : " + v
				changed = true
			}
		} else if ue4ss != "" {
			if n, was := modsTxtName(l); n == ue4ss {
				found = true
				if was != on {
					lines[i] = strings.TrimSpace(l[:strings.Index(l, ":")]) + " : " + v
					changed = true
				}
			}
		}
	}
	if !found && add {
		at := len(lines)
		for i, raw := range lines {
			if strings.Contains(strings.ToLower(raw), "keybinds") {
				at = i
				for at > 0 && strings.HasPrefix(strings.TrimSpace(lines[at-1]), ";") {
					at--
				}
				break
			}
		}
		lines = append(lines[:at], append([]string{name + " : " + v}, lines[at:]...)...)
		changed = true
	}
	if !changed {
		return nil
	}
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return os.WriteFile(p, []byte(strings.Join(lines, "\r\n")), 0o644)
}

func setModsJSON(modsDir, name string, on bool) {
	p := join(modsDir, "mods.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var list []map[string]any
	if json.Unmarshal(b, &list) != nil {
		return
	}
	changed := false
	for _, e := range list {
		if n, _ := e["mod_name"].(string); strings.EqualFold(n, name) {
			if v, _ := e["mod_enabled"].(bool); v != on {
				e["mod_enabled"] = on
				changed = true
			}
		}
	}
	if changed {
		nb, _ := json.Marshal(list)
		os.WriteFile(p, nb, 0o644)
	}
}
