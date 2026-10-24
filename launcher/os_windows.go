//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	shell32           = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
	user32            = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW   = user32.NewProc("MessageBoxW")
	advapi32          = syscall.NewLazyDLL("advapi32.dll")
	procRegGetValueW  = advapi32.NewProc("RegGetValueW")
)

var (
	platform = "windows"
	sysName  = "Windows"
	exeName  = "ACE8ModLauncher.exe"
)

func platformDataDir() string { return "" }
func platformStart()          {}
func hasPicker() bool         { return true }

func sameDrive(a, b string) bool {
	return strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b))
}

func findEdge() string {
	for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
		if base := os.Getenv(env); base != "" {
			p := filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

func shellOpen(target string) error {
	t, _ := syscall.UTF16PtrFromString(target)
	verb, _ := syscall.UTF16PtrFromString("open")
	r, _, _ := procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(t)), 0, 0, 1)
	if r <= 32 {
		return errors.New("Windows could not open " + target)
	}
	return nil
}

// openURL shows the program's window (an Edge app window) or opens a link (steam://…).
func openURL(url string, appWindow bool) error {
	if appWindow {
		if edge := findEdge(); edge != "" {
			cmd := exec.Command(edge, "--app="+url, "--window-size=1180,820")
			if err := cmd.Start(); err == nil {
				go cmd.Wait()
				return nil
			}
		}
	}
	return shellOpen(url)
}

// revealPath opens Explorer on a folder (or on the folder of a file, with the
// file selected) and brings that window in front of the launcher.
func revealPath(p string) error {
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	folder := p
	var cmd *exec.Cmd
	if st.IsDir() {
		cmd = exec.Command("explorer.exe", p)
	} else {
		folder = filepath.Dir(p)
		cmd = exec.Command("explorer.exe", "/select,", p)
	}
	before := explorerWindows()
	allowForeground()
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	go func() {
		// Windows opens the window behind the launcher (only the program you
		// clicked in may come to the front), so find it and bring it forward.
		name := strings.ToLower(filepath.Base(folder))
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			wins := explorerWindows()
			for h := range wins {
				if _, old := before[h]; !old {
					bringToFront(h)
					return
				}
			}
			if i < 12 {
				continue
			}
			// no new window after a moment: Explorer showed the folder in a
			// window (or tab) it already had open
			for h, title := range wins {
				tl := strings.ToLower(title)
				if tl == name || tl == strings.ToLower(folder) || strings.HasPrefix(tl, name+" - ") || strings.HasPrefix(tl, name+" and ") {
					bringToFront(h)
					return
				}
			}
		}
	}()
	return nil
}

var (
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procBringWindowToTop         = user32.NewProc("BringWindowToTop")
	procShowWindow               = user32.NewProc("ShowWindow")
	procIsIconic                 = user32.NewProc("IsIconic")
	procSwitchToThisWindow       = user32.NewProc("SwitchToThisWindow")
	procAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetCurrentThreadId       = kernel32.NewProc("GetCurrentThreadId")

	enumMu    sync.Mutex
	enumFound map[uintptr]string
	// one callback for the life of the program (Windows callbacks are limited)
	enumExplorer = syscall.NewCallback(func(h, _ uintptr) uintptr {
		var cls [64]uint16
		n, _, _ := procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&cls[0])), uintptr(len(cls)))
		if n == 0 || syscall.UTF16ToString(cls[:n]) != "CabinetWClass" {
			return 1
		}
		if v, _, _ := procIsWindowVisible.Call(h); v == 0 {
			return 1
		}
		var title [512]uint16
		m, _, _ := procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
		enumFound[h] = syscall.UTF16ToString(title[:m])
		return 1
	})
)

// explorerWindows lists the open Explorer windows and their titles.
func explorerWindows() map[uintptr]string {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumFound = map[uintptr]string{}
	procEnumWindows.Call(enumExplorer, 0)
	return enumFound
}

// allowForeground lets the next program started come to the front, when Windows allows the launcher to give that.
func allowForeground() {
	const asfwAny = ^uintptr(0) // ASFW_ANY
	procAllowSetForegroundWindow.Call(asfwAny)
}

// bringToFront puts a window in front, the way Alt+Tab does.
func bringToFront(h uintptr) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, _ := procIsIconic.Call(h); r != 0 {
		procShowWindow.Call(h, 9) // SW_RESTORE
	}
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == h {
		return
	}
	// Windows lets a program change the front window only while it shares input
	// with the window that is in front now.
	me, _, _ := procGetCurrentThreadId.Call()
	fgThread, _, _ := procGetWindowThreadProcessId.Call(fg, 0)
	if fgThread != 0 && fgThread != me {
		if r, _, _ := procAttachThreadInput.Call(me, fgThread, 1); r != 0 {
			defer procAttachThreadInput.Call(me, fgThread, 0)
		}
	}
	procSetForegroundWindow.Call(h)
	procBringWindowToTop.Call(h)
	if now, _, _ := procGetForegroundWindow.Call(); now != h {
		procSwitchToThisWindow.Call(h, 1)
	}
}

func processNames() map[string]bool {
	out := map[string]bool{}
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return out
	}
	defer syscall.CloseHandle(snap)
	var pe syscall.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = syscall.Process32First(snap, &pe); err == nil; err = syscall.Process32Next(snap, &pe) {
		out[strings.ToLower(syscall.UTF16ToString(pe.ExeFile[:]))] = true
	}
	return out
}

func processRunning(names ...string) bool {
	running := processNames()
	for _, n := range names {
		if running[strings.ToLower(n)] {
			return true
		}
	}
	return false
}

func steamRunning() bool { return processRunning("steam.exe") }

// startDetached starts a program that keeps running after the launcher closes.
// raw is added to the command line exactly as the player typed it, so quotes
// such as -ExecCmds="a b" reach the game unchanged.
func startDetached(exe, dir string, args []string, raw string, env []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	line := syscall.EscapeArg(exe)
	for _, a := range args {
		line += " " + syscall.EscapeArg(a)
	}
	if raw != "" {
		line += " " + raw
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, CreationFlags: 0x00000200} // CREATE_NEW_PROCESS_GROUP
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// startElevated starts a program the way Explorer does (ShellExecuteEx "open"),
// so Windows shows its permission prompt for a program that must run as
// administrator. Settings in the environment don't reach it.
func startElevated(exe, dir string, args []string, raw string) error {
	params := ""
	for _, a := range args {
		params += syscall.EscapeArg(a) + " "
	}
	params = strings.TrimSpace(params + raw)
	verb, _ := syscall.UTF16PtrFromString("open")
	file, _ := syscall.UTF16PtrFromString(exe)
	par, _ := syscall.UTF16PtrFromString(params)
	wd, _ := syscall.UTF16PtrFromString(dir)
	const seeNoCloseProcess, seeNoAsync, seeFlagNoUI = 0x40, 0x100, 0x400
	info := &shellExecuteInfo{fMask: seeNoCloseProcess | seeNoAsync | seeFlagNoUI, lpVerb: verb, lpFile: file,
		lpParameters: par, lpDirectory: wd, nShow: 1}
	info.cbSize = uint32(unsafe.Sizeof(*info))
	allowForeground()
	if r, _, err := procShellExecuteExW.Call(uintptr(unsafe.Pointer(info))); r == 0 {
		if errno, ok := err.(syscall.Errno); ok && errno != 0 {
			return errno
		}
		return errors.New("Windows could not start it")
	}
	if info.hProcess != 0 {
		syscall.CloseHandle(syscall.Handle(info.hProcess))
	}
	return nil
}

type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hIcon        uintptr
	hProcess     uintptr
}

var procShellExecuteExW = shell32.NewProc("ShellExecuteExW")

// runAsAdminSet: AceCombat8.exe has "Run this program as an administrator"
// ticked in its Compatibility settings (for this user or for all users).
func runAsAdminSet(exe string) bool {
	const hkcu, hklm = 0x80000001, 0x80000002
	key := `Software\Microsoft\Windows NT\CurrentVersion\AppCompatFlags\Layers`
	for _, root := range []uintptr{hkcu, hklm} {
		if strings.Contains(strings.ToUpper(regString(root, key, exe)), "RUNASADMIN") {
			return true
		}
	}
	return false
}

// steamActiveUser reads the account Steam is signed in with (0 while signing in).
// known is false when Steam doesn't keep that value on this PC.
func steamActiveUser() (user uint32, known bool) {
	const hkcu = 0x80000001
	const rrfRtDword = 0x00000010
	k, _ := syscall.UTF16PtrFromString(`Software\Valve\Steam\ActiveProcess`)
	v, _ := syscall.UTF16PtrFromString("ActiveUser")
	var val uint32
	size := uint32(4)
	r, _, _ := procRegGetValueW.Call(hkcu, uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(v)), rrfRtDword, 0,
		uintptr(unsafe.Pointer(&val)), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return 0, false
	}
	return val, true
}

func regString(root uintptr, key, value string) string {
	k, _ := syscall.UTF16PtrFromString(key)
	v, _ := syscall.UTF16PtrFromString(value)
	buf := make([]uint16, 1024)
	size := uint32(len(buf) * 2)
	const rrfRtRegSz = 0x00000002
	r, _, _ := procRegGetValueW.Call(root, uintptr(unsafe.Pointer(k)), uintptr(unsafe.Pointer(v)), rrfRtRegSz, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func registrySteamPaths() []string {
	const hkcu, hklm = 0x80000001, 0x80000002
	var out []string
	for _, p := range []string{
		regString(hkcu, `Software\Valve\Steam`, "SteamPath"),
		regString(hklm, `SOFTWARE\WOW6432Node\Valve\Steam`, "InstallPath"),
		regString(hklm, `SOFTWARE\Valve\Steam`, "InstallPath"),
	} {
		if p != "" {
			out = append(out, filepath.Clean(filepath.FromSlash(p)))
		}
	}
	return out
}

func vcRuntimeInstalled() bool {
	sys := filepath.Join(os.Getenv("SystemRoot"), "System32")
	return isFile(filepath.Join(sys, "vcruntime140_1.dll")) && isFile(filepath.Join(sys, "msvcp140.dll"))
}

func isCrossDevice(err error) bool { return errors.Is(err, syscall.Errno(17)) } // ERROR_NOT_SAME_DEVICE

func isSharingViolation(err error) bool {
	return errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(33)) // sharing / lock violation
}

func showFatal(msg string) {
	t, _ := syscall.UTF16PtrFromString(msg)
	c, _ := syscall.UTF16PtrFromString(appName)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), 0x10)
}
