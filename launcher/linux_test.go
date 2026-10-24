//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runs the real launch script in front of a fake Steam command that prints what it got
func runWrapper(t *testing.T, script string, env []string, args ...string) []string {
	t.Helper()
	cmd := exec.Command("sh", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

func TestLinuxWrapperScript(t *testing.T) {
	g := newGame(t)
	dir := t.TempDir()
	script := join(dir, wrapperName)
	write(t, script, wrapperScript())
	fake := join(dir, "reaper")
	write(t, fake, "#!/bin/sh\nfor a do echo \"arg:$a\"; done\necho \"dll:$WINEDLLOVERRIDES\"\n")
	os.Chmod(fake, 0o755)
	steamCmd := []string{fake, "SteamLaunch", "AppId=2288340", "--", "/proton", "waitforexitandrun", g.Bootstrapper(), "-dx11"}

	// Unmodded: Steam's command unchanged
	out := runWrapper(t, script, []string{"WINEDLLOVERRIDES="}, steamCmd...)
	want := []string{"arg:SteamLaunch", "arg:AppId=2288340", "arg:--", "arg:/proton", "arg:waitforexitandrun", "arg:" + g.Bootstrapper(), "arg:-dx11", "dll:"}
	if strings.Join(out, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unmodded:\n%s", strings.Join(out, "\n"))
	}
	ss := lastSteamStart(dir)
	if ss == nil || ss.Mode != "unmodded" || ss.Text != g.Root || time.Since(ss.When) > time.Minute {
		t.Fatalf("log: %+v", ss)
	}

	// Modded: AceCombat8.exe with anti-cheat off, and Proton loads dwmapi.dll
	installModding(t, g)
	out = runWrapper(t, script, []string{"WINEDLLOVERRIDES=d3d11=n"}, steamCmd...)
	want = []string{"arg:SteamLaunch", "arg:AppId=2288340", "arg:--", "arg:/proton", "arg:waitforexitandrun", "arg:" + g.GameExe(),
		"arg:-eac-nop-loaded", "arg:-nosplash", "arg:-dx11", "dll:dwmapi=n,b;d3d11=n"}
	if strings.Join(out, "\n") != strings.Join(want, "\n") {
		t.Fatalf("modded:\n%s", strings.Join(out, "\n"))
	}
	if ss := lastSteamStart(dir); ss == nil || ss.Mode != "modded" {
		t.Fatalf("log: %+v", ss)
	}
	// Steam already pointing at AceCombat8.exe, with the options there once
	out = runWrapper(t, script, nil, fake, g.GameExe(), "-nosplash")
	if strings.Join(out[:4], "|") != "arg:"+g.GameExe()+"|arg:-eac-nop-loaded|arg:-nosplash|dll:dwmapi=n,b" {
		t.Fatalf("direct: %v", out)
	}
	// something else: unchanged
	out = runWrapper(t, script, nil, fake, "/other/game.exe")
	if out[0] != "arg:/other/game.exe" {
		t.Fatal(out)
	}
	if ss := lastSteamStart(dir); ss == nil || ss.Mode != "unknown" {
		t.Fatalf("log: %+v", ss)
	}
	// run by hand: explains itself
	if b, err := exec.Command("sh", script).CombinedOutput(); err == nil || !strings.Contains(string(b), "%command%") {
		t.Fatalf("%v %s", err, b)
	}
}

func TestWithWrapper(t *testing.T) {
	s := "/home/me/.local/share/ace8-mod-launcher/" + wrapperName
	sp := "/home/me/My Games/" + wrapperName
	for _, c := range [][3]string{
		{"", s, s + " %command%"},
		{"-dx11", s, s + " %command% -dx11"},
		{"gamemoderun %command% -dx11", s, "gamemoderun " + s + " %command% -dx11"},
		{"PROTON_LOG=1 %command%", s, "PROTON_LOG=1 " + s + " %command%"},
		{"/old/place/" + wrapperName + " %command% -dx11", s, s + " %command% -dx11"},
		{`"` + sp + `" %command%`, s, s + " %command%"},
		{"", sp, `"` + sp + `" %command%`},
	} {
		if got := withWrapper(c[0], c[1]); got != c[2] {
			t.Errorf("withWrapper(%q) = %q, want %q", c[0], got, c[2])
		}
	}
}

const localConfig = `"UserLocalConfigStore"
{
	"Software"
	{
		"Valve"
		{
			"Steam"
			{
				"apps"
				{
					"1245620"
					{
						"LaunchOptions"		"-x"
					}
%s
				}
				"other"		"keep"
			}
		}
	}
	"friends"	{ "a" "b" }
}
`

func TestVdfSet(t *testing.T) {
	path := []string{"UserLocalConfigStore", "Software", "Valve", "Steam", "apps", steamAppID}
	value := `/x/` + wrapperName + ` %command% "-a b"`
	check := func(src string) string {
		t.Helper()
		out, err := vdfSet(src, path, "LaunchOptions", value)
		if err != nil {
			t.Fatal(err)
		}
		root, err := parseVDF(out)
		if err != nil {
			t.Fatal(err)
		}
		if v := root.path(path...).value("LaunchOptions"); v != value {
			t.Fatalf("got %q in\n%s", v, out)
		}
		if root.path(path[:5]...).child("1245620").value("LaunchOptions") != "-x" || root.path(path[:4]...).value("other") != "keep" || root.path("UserLocalConfigStore", "friends").value("a") != "b" {
			t.Fatalf("other settings changed:\n%s", out)
		}
		return out
	}
	// value there: replaced
	check(strings.Replace(localConfig, "%s", "\t\t\t\t\t\""+steamAppID+"\"\n\t\t\t\t\t{\n\t\t\t\t\t\t\"LastPlayed\"\t\t\"1\"\n\t\t\t\t\t\t\"LaunchOptions\"\t\t\"old\"\n\t\t\t\t\t}", 1))
	// block there without it: added
	out := check(strings.Replace(localConfig, "%s", "\t\t\t\t\t\""+steamAppID+"\"\n\t\t\t\t\t{\n\t\t\t\t\t\t\"LastPlayed\"\t\t\"1\"\n\t\t\t\t\t}", 1))
	if r, _ := parseVDF(out); r.path(path...).value("LastPlayed") != "1" {
		t.Fatal("LastPlayed lost")
	}
	// no block: made
	check(strings.Replace(localConfig, "%s", "", 1))
	// no apps at all
	if _, err := vdfSet(`"UserLocalConfigStore" { }`, path, "LaunchOptions", "x"); err == nil {
		t.Fatal("no error without apps")
	}
}

func TestProcMatches(t *testing.T) {
	names := []string{"AceCombat8.exe", "start_protected_game.exe"}
	for _, c := range []struct {
		comm, arg0 string
		want       bool
	}{
		{"AceCombat8.exe", `Z:\games\ACE COMBAT 8\Game\Binaries\Win64\AceCombat8.exe`, true},
		{"wine64-preload", `C:\x\AceCombat8.exe`, true},
		{"start_protected", "", true},
		{"start_protecte", "", false},
		{"steam", "/home/me/.local/share/Steam/ubuntu12_32/steam", false},
	} {
		if got := procMatches(c.comm, c.arg0, names); got != c.want {
			t.Errorf("%q %q: %v", c.comm, c.arg0, got)
		}
	}
	if !procMatches("steam", "", []string{"steam"}) || procMatches("steamwebhelper", "", []string{"steam"}) {
		t.Error("steam")
	}
}

func TestLinuxSteamPlaces(t *testing.T) {
	home := "/home/me"
	data := "/home/me/.local/share/ace8-mod-launcher"
	if d := wrapperDir("/home/me/.local/share/Steam", home, data); d != data {
		t.Error(d)
	}
	if d := wrapperDir("/home/me/.var/app/com.valvesoftware.Steam/.local/share/Steam", home, data); d != "/home/me/.var/app/com.valvesoftware.Steam/ace8-mod-launcher" {
		t.Error(d)
	}
	if d := wrapperDir("/home/me/snap/steam/common/.local/share/Steam", home, data); d != "/home/me/snap/steam/common/ace8-mod-launcher" {
		t.Error(d)
	}
	if len(linuxSteamDirs(home)) < 4 || len(linuxSteamDirs("")) != 0 {
		t.Error("steam dirs")
	}
	e := desktopEntry("/home/me/My Apps/ACE8ModLauncher", "/x/icon.png")
	if !strings.Contains(e, `Exec="/home/me/My Apps/ACE8ModLauncher"`) || !strings.Contains(e, "Icon=/x/icon.png") {
		t.Error(e)
	}
}

// the whole Linux flow: Play needs the launch options; the launcher sets them
// with Steam closed; then Play goes through Steam in both modes.
func TestLinuxPlay(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	platform = "linux"
	defer func() { platform = "windows" }()
	steamHome := t.TempDir()
	os.MkdirAll(join(steamHome, "steamapps"), 0o755)
	cfg := join(steamHome, "userdata", "123", "config", "localconfig.vdf")
	write(t, cfg, strings.Replace(localConfig, "%s", "\t\t\t\t\t\""+steamAppID+"\"\n\t\t\t\t\t{\n\t\t\t\t\t\t\"LaunchOptions\"\t\t\"-dx11\"\n\t\t\t\t\t}", 1))
	os.Setenv("ACE8_FAKE_STEAM", steamHome)

	if _, err := g.play(); err != errLaunchOptionMissing {
		t.Fatalf("modded without the launch option: %v", err)
	}
	if !isFile(wrapperPath()) {
		t.Fatal("script not written")
	}
	if st, _ := os.Stat(wrapperPath()); st.Mode().Perm()&0o100 == 0 {
		t.Fatal("script not executable")
	}
	if filepath.Dir(wrapperPath()) != dataDir {
		t.Fatal(wrapperPath())
	}
	ll := linuxLaunchState()
	if ll.Set || ll.Current != "-dx11" || ll.Suggested != shellWord(wrapperPath())+" %command% -dx11" {
		t.Fatalf("%+v", ll)
	}
	// Steam running: can't change its settings
	if _, err := setSteamLaunchOptions(); err == nil || !strings.Contains(err.Error(), "close Steam") {
		t.Fatal(err)
	}
	os.Setenv("ACE8_FAKE_NO_STEAM", "1")
	if n, err := setSteamLaunchOptions(); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	os.Unsetenv("ACE8_FAKE_NO_STEAM")
	if !isFile(cfg + ".ace8-backup") {
		t.Fatal("no backup")
	}
	ll = linuxLaunchState()
	if !ll.Set || ll.Current != ll.Suggested {
		t.Fatalf("%+v", ll)
	}
	os.Remove(join(dataDir, "opened.txt"))
	li, err := g.play()
	if err != nil || li.How != "steam" {
		t.Fatal(li, err)
	}
	o, _ := os.ReadFile(join(dataDir, "opened.txt"))
	if !strings.Contains(string(o), "steam://rungameid/2288340") || exists(join(dataDir, "started.txt")) {
		t.Fatalf("modded on Linux goes through Steam: %s", o)
	}
	// the window learns about it
	st := buildState()
	if st.Platform != "linux" || st.Linux == nil || !st.Linux.Set {
		t.Fatalf("%+v", st.Linux)
	}
	// Unmodded doesn't need the launch option
	write(t, cfg, strings.Replace(localConfig, "%s", "", 1))
	g.switchTo(modeUnmodded)
	if _, err := g.play(); err != nil {
		t.Fatal(err)
	}
	// forced Modded start (Steam hasn't saved the options yet)
	g.switchTo(modeModded)
	if _, err := g.playWith(true); err != nil {
		t.Fatal(err)
	}
	if r := supportReport(); !strings.Contains(r, "launch script") {
		t.Fatal("report has no Linux part")
	}
}
