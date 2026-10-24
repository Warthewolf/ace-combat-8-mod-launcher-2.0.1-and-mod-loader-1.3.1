//go:build linux && linuxapp

package main

// The Linux version (built with -tags linuxapp). The window opens in a
// Chromium-family browser as an app window when there is one, otherwise in the
// default browser; folders open in the desktop's file manager; folder and file
// choosers come from zenity or kdialog (KDE, Steam Deck).

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

var (
	platform = "linux"
	sysName  = "Linux"
	exeName  = "ACE8ModLauncher"
)

func platformDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "ace8-mod-launcher")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "share", "ace8-mod-launcher")
	}
	return ""
}

// platformStart writes the Steam launch script and the app menu entry.
func platformStart() {
	if err := writeWrapper(); err != nil {
		logf("could not write the Steam launch script: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	icon := filepath.Join(dataDir, "icon.png")
	if b, err := uiFiles.ReadFile("ui/icon.png"); err == nil {
		if old, err := os.ReadFile(icon); err != nil || string(old) != string(b) {
			os.WriteFile(icon, b, 0o644)
		}
	}
	apps := os.Getenv("XDG_DATA_HOME")
	if apps == "" {
		if h, err := os.UserHomeDir(); err == nil {
			apps = filepath.Join(h, ".local", "share")
		}
	}
	if apps == "" {
		return
	}
	apps = filepath.Join(apps, "applications")
	p := filepath.Join(apps, "ace8-mod-launcher.desktop")
	want := desktopEntry(exe, icon)
	if old, err := os.ReadFile(p); err == nil && string(old) == want {
		return
	}
	if os.MkdirAll(apps, 0o755) == nil && os.WriteFile(p, []byte(want), 0o755) == nil {
		logf("app menu entry: %s", p)
	}
}

func onPath(name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

// start runs a program on its own: it keeps running when the launcher stops.
func start(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = null, null, null
		defer null.Close()
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

func flatpakInstalled(id string) bool {
	if onPath("flatpak") == "" {
		return false
	}
	home, _ := os.UserHomeDir()
	return isDir(filepath.Join("/var/lib/flatpak/app", id)) || (home != "" && isDir(filepath.Join(home, ".local/share/flatpak/app", id)))
}

// appBrowser finds a browser that can show the launcher as its own window.
func appBrowser() (string, []string) {
	for _, n := range []string{"microsoft-edge", "microsoft-edge-stable", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "brave-browser", "brave", "vivaldi", "vivaldi-stable"} {
		if p := onPath(n); p != "" {
			return p, nil
		}
	}
	for _, id := range []string{"com.microsoft.Edge", "com.google.Chrome", "org.chromium.Chromium", "com.brave.Browser", "com.vivaldi.Vivaldi"} {
		if flatpakInstalled(id) {
			return "flatpak", []string{"run", id}
		}
	}
	return "", nil
}

// openURL shows the program's window or opens a link (steam://…, https://…).
func openURL(u string, appWindow bool) error {
	if appWindow {
		if b, pre := appBrowser(); b != "" {
			if err := start(b, append(pre, "--app="+u, "--window-size=1180,820")...); err == nil {
				return nil
			}
		}
	}
	if strings.HasPrefix(u, "steam://") {
		if p := onPath("steam"); p != "" {
			return start(p, u)
		}
		if flatpakInstalled("com.valvesoftware.Steam") {
			return start("flatpak", "run", "com.valvesoftware.Steam", u)
		}
	}
	if p := onPath("xdg-open"); p != "" {
		return start(p, u)
	}
	if p := onPath("gio"); p != "" {
		return start(p, "open", u)
	}
	return errors.New("no program was found to open " + u)
}

// revealPath shows a folder (or a file in its folder) in the file manager.
func revealPath(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		if d := onPath("dbus-send"); d != "" {
			fileURL := (&url.URL{Scheme: "file", Path: p}).String()
			cmd := exec.Command(d, "--session", "--print-reply", "--dest=org.freedesktop.FileManager1", "--type=method_call",
				"/org/freedesktop/FileManager1", "org.freedesktop.FileManager1.ShowItems", "array:string:"+fileURL, "string:")
			if cmd.Run() == nil {
				return nil
			}
		}
		p = filepath.Dir(p)
	}
	return openURL(p, false)
}

func hasPicker() bool { return onPath("kdialog") != "" || onPath("zenity") != "" }

// pickerFirst: KDE (and the Steam Deck) gets kdialog, everything else zenity.
func pickerFirst() []string {
	desk := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP") + os.Getenv("DESKTOP_SESSION"))
	if strings.Contains(desk, "kde") || strings.Contains(desk, "plasma") {
		return []string{"kdialog", "zenity"}
	}
	return []string{"zenity", "kdialog"}
}

var errNoPicker = errors.New("no folder chooser was found on this computer (zenity or kdialog). Type the folder's path instead, or drop the mod onto the window")

// runPicker returns what the chooser printed; nothing when it was cancelled.
func runPicker(args map[string][]string) (string, error) {
	for _, name := range pickerFirst() {
		p := onPath(name)
		if p == "" {
			continue
		}
		out, err := exec.Command(p, args[name]...).Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == 1 {
				return "", nil // cancelled
			}
			return "", fmt.Errorf("%s did not work (%v)", name, err)
		}
		return strings.TrimRight(string(out), "\r\n"), nil
	}
	return "", errNoPicker
}

func pickFolder(title, startDir string, allowNew bool) (string, error) {
	if startDir == "" || !isDir(startDir) {
		startDir, _ = os.UserHomeDir()
	}
	out, err := runPicker(map[string][]string{
		"zenity":  {"--file-selection", "--directory", "--title=" + title, "--filename=" + strings.TrimSuffix(startDir, "/") + "/"},
		"kdialog": {"--title", title, "--getexistingdirectory", startDir},
	})
	return strings.TrimSpace(out), err
}

func pickFiles(title string) ([]string, error) {
	home, _ := os.UserHomeDir()
	start := home
	if d := filepath.Join(home, "Downloads"); isDir(d) {
		start = d
	}
	out, err := runPicker(map[string][]string{
		"zenity":  {"--file-selection", "--multiple", "--separator=\n", "--title=" + title, "--filename=" + start + "/"},
		"kdialog": {"--title", title, "--getopenfilename", start, "--multiple", "--separate-output"},
	})
	if err != nil || strings.TrimSpace(out) == "" {
		return nil, err
	}
	var ps []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			ps = append(ps, l)
		}
	}
	return ps, nil
}

// processRunning looks through /proc. Wine shows Windows programs by their .exe name.
func processRunning(names ...string) bool {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		comm, _ := os.ReadFile("/proc/" + e.Name() + "/comm")
		cmdline, _ := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		arg0 := string(cmdline)
		if i := strings.IndexByte(arg0, 0); i >= 0 {
			arg0 = arg0[:i]
		}
		if procMatches(strings.TrimSpace(string(comm)), arg0, names) {
			return true
		}
	}
	return false
}

func steamRunning() bool { return processRunning("steam") }

// Starting AceCombat8.exe directly is for Windows; on Linux Steam starts it.
func startDetached(exe, dir string, args []string, raw string, env []string) error {
	return errors.New("on Linux the game is started through Steam")
}

func startElevated(exe, dir string, args []string, raw string) error {
	return errors.New("on Linux the game is started through Steam")
}

func runAsAdminSet(exe string) bool   { return false }
func steamActiveUser() (uint32, bool) { return 0, false }
func vcRuntimeInstalled() bool        { return true } // Proton brings its own
func isCrossDevice(err error) bool    { return errors.Is(err, syscall.EXDEV) }
func isSharingViolation(err error) bool {
	return errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.ETXTBSY)
}

func registrySteamPaths() []string {
	home, _ := os.UserHomeDir()
	var out []string
	seen := map[string]bool{}
	for _, p := range linuxSteamDirs(home) {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		if !seen[p] && isDir(filepath.Join(p, "steamapps")) {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// sameDrive: the same file system, so moving between them is instant.
func sameDrive(a, b string) bool {
	da, oka := deviceOf(a)
	db, okb := deviceOf(b)
	return !oka || !okb || da == db
}

func deviceOf(p string) (uint64, bool) {
	for d := filepath.Clean(p); ; d = filepath.Dir(d) {
		var st syscall.Stat_t
		if syscall.Stat(d, &st) == nil {
			return uint64(st.Dev), true
		}
		if filepath.Dir(d) == d {
			return 0, false
		}
	}
}

func showFatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	if p := onPath("zenity"); p != "" {
		exec.Command(p, "--error", "--title=ACE8 Mod Launcher", "--text="+msg).Run()
	} else if p := onPath("kdialog"); p != "" {
		exec.Command(p, "--title", "ACE8 Mod Launcher", "--error", msg).Run()
	}
}
