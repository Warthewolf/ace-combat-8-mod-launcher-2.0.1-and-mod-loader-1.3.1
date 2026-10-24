//go:build !windows

package main

// Linux and Steam Deck.
//
// On Linux the game runs through Steam's Proton, so the launcher can't start
// AceCombat8.exe itself the way it does on Windows. Instead, a small script goes
// in front of Steam's own command, through the game's Steam launch options:
//
//	"/home/me/.local/share/ace8-mod-launcher/ace8-steam-launch.sh" %command%
//
// The script looks at the game folder each time Steam starts the game. Modded
// (UE4SS's dwmapi.dll is in the game): it swaps the anti-cheat bootstrapper for
// AceCombat8.exe with -eac-nop-loaded -nosplash, like ac8noeac.bat, and tells
// Proton to load UE4SS's dwmapi.dll. Unmodded: Steam's command runs unchanged.
// So the launch option can stay set for good, and Steam's own Play button (and
// Game Mode on the Steam Deck) always does the right thing.
//
// Everything here is plain logic, so the tests run it on any system; the parts
// that need the desktop (browser, folder pickers, processes) are in os_linux.go.

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const wrapperName = "ace8-steam-launch.sh"

// wrapperScript is the script Steam runs in front of the game.
func wrapperScript() string {
	return `#!/bin/sh
# ACE8 Mod Launcher ` + appVersion + `: starts ACE COMBAT 8 from Steam on Linux and Steam Deck.
#
# Steam launch options for ACE COMBAT 8 (Properties > General > Launch Options):
#   "this file's full path" %command%
#
# Modded (UE4SS's dwmapi.dll is in the game): the game starts directly with
# anti-cheat off (AceCombat8.exe -eac-nop-loaded -nosplash, like ac8noeac.bat on
# Windows), and Proton loads UE4SS. Offline and single player only.
# Unmodded: Steam's own command runs unchanged, with anti-cheat on.
#
# ACE8 Mod Launcher writes this file each time it starts; changes are lost.

if [ $# -eq 0 ]; then
  echo "Put this in Steam's launch options for ACE COMBAT 8:  \"$0\" %command%"
  exit 1
fi

here=$(dirname "$0")
log="$here/steam-launch.log"
if [ -f "$log" ] && [ "$(wc -c < "$log")" -gt 200000 ]; then
  mv -f "$log" "$log.old" 2>/dev/null
fi
now=$(date '+%Y-%m-%d %H:%M:%S' 2>/dev/null)

root=""
for a do
  case "$a" in
    */start_protected_game.exe) root=${a%/*} ;;
    */Game/Binaries/Win64/AceCombat8.exe) root=${a%/Game/Binaries/Win64/AceCombat8.exe} ;;
  esac
done

if [ -z "$root" ]; then
  echo "$now unknown AceCombat8.exe was not in Steam's command: $*" >> "$log" 2>/dev/null
  exec "$@"
fi

if [ ! -f "$root/Game/Binaries/Win64/dwmapi.dll" ]; then
  echo "$now unmodded $root" >> "$log" 2>/dev/null
  exec "$@"
fi

exe="$root/Game/Binaries/Win64/AceCombat8.exe"
first=1
for a do
  if [ "$first" = 1 ]; then set --; first=0; fi
  case "$a" in
    */start_protected_game.exe|*/Game/Binaries/Win64/AceCombat8.exe)
      set -- "$@" "$exe" -eac-nop-loaded -nosplash ;;
    -eac-nop-loaded|-nosplash) ;;
    *) set -- "$@" "$a" ;;
  esac
done
WINEDLLOVERRIDES="dwmapi=n,b${WINEDLLOVERRIDES:+;$WINEDLLOVERRIDES}"
export WINEDLLOVERRIDES
echo "$now modded $root" >> "$log" 2>/dev/null
exec "$@"
`
}

// linuxSteamDirs: where Steam keeps its files on Linux: the normal install, the
// Flatpak and the Snap.
func linuxSteamDirs(home string) []string {
	if home == "" {
		return nil
	}
	return []string{
		join(home, ".steam", "steam"),
		join(home, ".local", "share", "Steam"),
		join(home, ".steam", "root"),
		join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam"),
		join(home, ".var", "app", "com.valvesoftware.Steam", "data", "Steam"),
		join(home, "snap", "steam", "common", ".local", "share", "Steam"),
	}
}

// wrapperDir is where the script goes: somewhere Steam can reach. The Flatpak
// and Snap versions of Steam can't see the rest of the home folder.
func wrapperDir(steam, home, data string) string {
	switch {
	case home != "" && strings.Contains(steam, "/.var/app/com.valvesoftware.Steam/"):
		return join(home, ".var", "app", "com.valvesoftware.Steam", "ace8-mod-launcher")
	case home != "" && strings.Contains(steam, "/snap/steam/"):
		return join(home, "snap", "steam", "common", "ace8-mod-launcher")
	}
	return data
}

func wrapperPath() string {
	home, _ := os.UserHomeDir()
	return join(wrapperDir(steamDir(), home, dataDir), wrapperName)
}

// writeWrapper puts the script in place (again), so it always matches this
// version of the launcher.
func writeWrapper() error {
	p := wrapperPath()
	if b, err := os.ReadFile(p); err == nil && string(b) == wrapperScript() {
		if st, err := os.Stat(p); err == nil && st.Mode().Perm()&0o111 != 0 {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".new"
	if err := os.WriteFile(tmp, []byte(wrapperScript()), 0o755); err != nil {
		return err
	}
	os.Chmod(tmp, 0o755)
	return os.Rename(tmp, p)
}

// launchLine is what goes in Steam's launch options.
func launchLine(script string) string {
	return shellWord(script) + " %command%"
}

func shellWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\$`;&|<>()*?[]#~") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(s) + `"`
}

var reOurWrapper = regexp.MustCompile(`("(?:[^"\\]|\\.)*` + regexp.QuoteMeta(wrapperName) + `"|\S*` + regexp.QuoteMeta(wrapperName) + `)\s*`)

// withWrapper returns Steam launch options with the launcher's script in them,
// keeping whatever else the player has there (gamemoderun, PROTON_… settings,
// game options).
func withWrapper(existing, script string) string {
	rest := strings.TrimSpace(reOurWrapper.ReplaceAllString(existing, ""))
	word := shellWord(script)
	if strings.Contains(rest, "%command%") {
		return strings.Replace(rest, "%command%", word+" %command%", 1)
	}
	if rest == "" {
		return word + " %command%"
	}
	return word + " %command% " + rest
}

// LinuxLaunch is what the window shows about starting the game on Linux.
type LinuxLaunch struct {
	Script    string      `json:"script"`
	Line      string      `json:"line"`      // what goes in Steam's launch options
	Set       bool        `json:"set"`       // Steam's launch options have it
	Stale     bool        `json:"stale"`     // they have the script, but at another place
	Current   string      `json:"current"`   // Steam's launch options now
	Suggested string      `json:"suggested"` // the player's launch options with the script added
	LastStart *SteamStart `json:"lastStart,omitempty"`
}

type SteamStart struct {
	When time.Time `json:"when"`
	Mode string    `json:"mode"` // modded, unmodded, unknown
	Text string    `json:"text"`
}

func linuxLaunchState() *LinuxLaunch {
	script := wrapperPath()
	ll := &LinuxLaunch{Script: script, Line: launchLine(script)}
	opts := steamLaunchOptions()
	for _, o := range opts {
		if strings.Contains(o, wrapperName) {
			if strings.Contains(o, shellWord(script)) || strings.Contains(o, script) {
				ll.Set = true
			} else {
				ll.Stale = true
			}
		}
	}
	if ll.Set {
		ll.Stale = false
	}
	if len(opts) > 0 {
		ll.Current = opts[0]
	}
	ll.Suggested = withWrapper(ll.Current, script)
	ll.LastStart = lastSteamStart(filepath.Dir(script))
	return ll
}

// lastSteamStart reads the last line the script wrote.
func lastSteamStart(dir string) *SteamStart {
	f, err := os.Open(join(dir, "steam-launch.log"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var last string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			last = sc.Text()
		}
	}
	parts := strings.SplitN(last, " ", 4)
	if len(parts) < 3 {
		return nil
	}
	t, err := time.ParseInLocation("2006-01-02 15:04:05", parts[0]+" "+parts[1], time.Local)
	if err != nil {
		return nil
	}
	ss := &SteamStart{When: t, Mode: parts[2]}
	if len(parts) == 4 {
		ss.Text = parts[3]
	}
	return ss
}

func steamStartLogTime() time.Time {
	st, err := os.Stat(join(filepath.Dir(wrapperPath()), "steam-launch.log"))
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// steamConfigStamp changes when Steam saves its settings, so the window notices
// new launch options.
func steamConfigStamp() string {
	sd := steamDir()
	if sd == "" {
		return ""
	}
	var b strings.Builder
	users, _ := os.ReadDir(join(sd, "userdata"))
	for _, u := range users {
		if st, err := os.Stat(join(sd, "userdata", u.Name(), "config", "localconfig.vdf")); err == nil {
			fmt.Fprintf(&b, ".%d", st.ModTime().UnixNano())
		}
	}
	return b.String()
}

// setSteamLaunchOptions puts the script into the game's launch options for
// every Steam account on this computer. Steam keeps its settings in memory and
// writes them when it closes, so it has to be closed first.
func setSteamLaunchOptions() (int, error) {
	if steamRunning() {
		return 0, errors.New("close Steam first (Steam menu, Exit), then press this again. Or copy the line and paste it into Steam yourself")
	}
	sd := steamDir()
	if sd == "" {
		return 0, errors.New("Steam's folder was not found")
	}
	if err := writeWrapper(); err != nil {
		return 0, fmt.Errorf("the launch script could not be written (%v)", err)
	}
	script := wrapperPath()
	users, _ := os.ReadDir(join(sd, "userdata"))
	n := 0
	var lastErr error
	for _, u := range users {
		p := join(sd, "userdata", u.Name(), "config", "localconfig.vdf")
		b, err := os.ReadFile(p)
		if !u.IsDir() || err != nil {
			continue
		}
		root, err := parseVDF(string(b))
		if err != nil {
			continue
		}
		current := ""
		if a := root.path("UserLocalConfigStore", "Software", "Valve", "Steam", "apps", steamAppID); a != nil {
			current = a.value("LaunchOptions")
		}
		out, err := vdfSet(string(b), []string{"UserLocalConfigStore", "Software", "Valve", "Steam", "apps", steamAppID}, "LaunchOptions", withWrapper(current, script))
		if err != nil {
			lastErr = err
			continue
		}
		if !isFile(p + ".ace8-backup") {
			os.WriteFile(p+".ace8-backup", b, 0o644)
		}
		tmp := p + ".ace8-new"
		if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
			lastErr = err
			continue
		}
		if err := os.Rename(tmp, p); err != nil {
			lastErr = err
			continue
		}
		n++
		logf("Steam launch options set for account %s", u.Name())
	}
	if n == 0 {
		if lastErr != nil {
			return 0, fmt.Errorf("Steam's settings could not be changed (%v). Copy the line and paste it into Steam yourself", lastErr)
		}
		return 0, errors.New("no Steam account was found on this computer. Copy the line and paste it into Steam yourself")
	}
	return n, nil
}

// ---- changing one value in a Valve KeyValues text file -----------------------------

type vdfTok struct {
	s          string
	quoted     bool
	start, end int // in the text, quotes included
}

func vdfTokens(s string) []vdfTok {
	var out []vdfTok
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '{' || c == '}':
			out = append(out, vdfTok{s: string(c), start: i, end: i + 1})
			i++
		case c == '"':
			st := i
			i++
			var b strings.Builder
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
					switch s[i] {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(s[i])
					}
					i++
					continue
				}
				b.WriteByte(s[i])
				i++
			}
			i++
			if i > len(s) {
				i = len(s)
			}
			out = append(out, vdfTok{s: b.String(), quoted: true, start: st, end: i})
		default:
			st := i
			for i < len(s) && !strings.ContainsRune(" \t\r\n{}\"", rune(s[i])) {
				i++
			}
			out = append(out, vdfTok{s: s[st:i], start: st, end: i})
		}
	}
	return out
}

func vdfQuote(v string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
}

// vdfSet sets key to value in the block at path, changing only that part of the
// text. A missing last block on the path is made.
func vdfSet(src string, path []string, key, value string) (string, error) {
	toks := vdfTokens(src)
	var stack []string
	at := func(p []string) bool {
		if len(stack) != len(p) {
			return false
		}
		for i := range p {
			if !strings.EqualFold(stack[i], p[i]) {
				return false
			}
		}
		return true
	}
	valStart, valEnd, open, parentOpen := -1, -1, -1, -1
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if !t.quoted && t.s == "}" {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		if !t.quoted && t.s == "{" {
			return "", errors.New("Steam's settings file is not in the expected format")
		}
		if i+1 >= len(toks) {
			break
		}
		next := toks[i+1]
		if !next.quoted && next.s == "{" {
			stack = append(stack, t.s)
			if open < 0 && at(path) {
				open = next.start
			}
			if parentOpen < 0 && at(path[:len(path)-1]) {
				parentOpen = next.start
			}
			i++
			continue
		}
		if !next.quoted && next.s == "}" {
			continue
		}
		if valStart < 0 && at(path) && strings.EqualFold(t.s, key) {
			valStart, valEnd = next.start, next.end
		}
		i++
	}
	tabs := func(n int) string { return strings.Repeat("\t", n) }
	kv := vdfQuote(key) + "\t\t" + vdfQuote(value)
	switch {
	case valStart >= 0:
		return src[:valStart] + vdfQuote(value) + src[valEnd:], nil
	case open >= 0:
		return src[:open+1] + "\n" + tabs(len(path)) + kv + src[open+1:], nil
	case parentOpen >= 0:
		d := len(path) - 1
		block := "\n" + tabs(d+1) + vdfQuote(path[len(path)-1]) + "\n" + tabs(d+1) + "{\n" + tabs(d+2) + kv + "\n" + tabs(d+1) + "}"
		return src[:parentOpen+1] + block + src[parentOpen+1:], nil
	}
	return "", errors.New("start the game from Steam once first, so Steam has settings for it")
}

// ---- processes ---------------------------------------------------------------------

// procMatches: a Linux process (its short name and first argument) is one of
// names. Wine shows Windows programs with their .exe name; the short name is cut
// to 15 characters.
func procMatches(comm, arg0 string, names []string) bool {
	base := arg0
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	for _, n := range names {
		if strings.EqualFold(comm, n) || strings.EqualFold(base, n) {
			return true
		}
		if len(comm) == 15 && len(n) > 15 && strings.EqualFold(n[:15], comm) {
			return true
		}
	}
	return false
}

// desktopEntry is the menu entry the launcher adds for itself on Linux, so it can
// be started from the app menu and added to Steam as a non-Steam game.
func desktopEntry(exe, icon string) string {
	return "[Desktop Entry]\nType=Application\nName=ACE8 Mod Launcher\nComment=Play Ace Combat 8 with or without mods\n" +
		"Exec=" + desktopQuote(exe) + "\nIcon=" + icon + "\nTerminal=false\nCategories=Game;\n"
}

func desktopQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\$`") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\\\`, `"`, `\"`, "`", "\\`", `$`, `\$`).Replace(s) + `"`
}
