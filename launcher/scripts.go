package main

// UE4SS script mods (Lua or C++), such as RoGLaser, SkinSlotText or SkyLive.
// UE4SS starts a mod when its folder has enabled.txt or mods.txt says "Name : 1";
// "Name : 0" in mods.txt switches it off.

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"
)

type ScriptMod struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Enabled     bool   `json:"enabled"`
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
}

var notScriptMods = []string{"IoStoreLoaderMod", "shared", "removed-mods"}

var reModsTxt = regexp.MustCompile(`^\s*([^:;]+?)\s*:\s*([01])\s*$`)

// modsTxtState reads mods.txt: name (lower case) -> on/off.
func modsTxtState(modsDir string) map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(join(modsDir, "mods.txt"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(decodeText(b), "\n") {
		if m := reModsTxt.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
			out[strings.ToLower(m[1])] = m[2] == "1"
		}
	}
	return out
}

func listScriptMods(ue4ssRoot string) []*ScriptMod {
	modsDir := join(ue4ssRoot, "Mods")
	ents, _ := os.ReadDir(modsDir)
	txt := modsTxtState(modsDir)
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
		lua, dll := isFile(join(dir, "Scripts", "main.lua")), isFile(join(dir, "dlls", "main.dll"))
		if !lua && !dll {
			continue
		}
		m := &ScriptMod{Name: e.Name(), Path: dir}
		switch {
		case lua && dll:
			m.Kind = "Lua + C++"
		case lua:
			m.Kind = "Lua"
		default:
			m.Kind = "C++"
		}
		on, listed := txt[strings.ToLower(e.Name())]
		hasEnabled := isFile(join(dir, "enabled.txt"))
		m.Enabled = (listed && on) || (hasEnabled && !(listed && !on))
		if lua {
			m.Description = luaDescription(join(dir, "Scripts", "main.lua"))
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
	if err := setModsTxtLine(modsDir, name, on, false); err != nil {
		return err
	}
	setModsJSON(modsDir, name, on)
	return nil
}

// setModsTxtLine changes "Name : 0/1" in mods.txt. With add, a missing line is added
// (above the Keybinds line UE4SS asks to keep last).
func setModsTxtLine(modsDir, name string, on, add bool) error {
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
