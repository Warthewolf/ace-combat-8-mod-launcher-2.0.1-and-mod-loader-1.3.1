//go:build !windows && !linuxapp

package main

// Stand-ins used when the launcher is built for tests on Linux. Everything the
// real program does with Windows is recorded in files instead.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The tests run as the Windows version; a test can switch platform to "linux".
var (
	platform = "windows"
	sysName  = "Windows"
	exeName  = "ACE8ModLauncher.exe"
)

func platformDataDir() string { return "" }
func platformStart()          {}
func hasPicker() bool         { return os.Getenv("ACE8_FAKE_NO_PICKER") == "" }

func sameDrive(a, b string) bool {
	return strings.EqualFold(filepath.VolumeName(a), filepath.VolumeName(b))
}

func record(file, line string) {
	f, err := os.OpenFile(filepath.Join(dataDir, file), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		fmt.Fprintln(f, time.Now().Format("15:04:05")+" "+line)
		f.Close()
	}
}

func openURL(url string, appWindow bool) error {
	record("opened.txt", url)
	return nil
}

func revealPath(p string) error {
	if !exists(p) {
		return os.ErrNotExist
	}
	record("revealed.txt", p)
	return nil
}

func pickFolder(title, start string, allowNew bool) (string, error) {
	return strings.TrimSpace(os.Getenv("ACE8_FAKE_PICK")), nil
}

func pickFiles(title string) ([]string, error) {
	v := strings.TrimSpace(os.Getenv("ACE8_FAKE_PICK"))
	if v == "" {
		return nil, nil
	}
	return strings.Split(v, "|"), nil
}

// ACE8_FAKE_RUNNING names a file listing the "running" programs, one per line.
func processRunning(names ...string) bool {
	p := os.Getenv("ACE8_FAKE_RUNNING")
	if p == "" {
		return false
	}
	b, _ := os.ReadFile(p)
	for _, l := range strings.Split(string(b), "\n") {
		for _, n := range names {
			if strings.EqualFold(strings.TrimSpace(l), n) {
				return true
			}
		}
	}
	return false
}

func steamRunning() bool { return os.Getenv("ACE8_FAKE_NO_STEAM") == "" || processRunning("steam.exe") }

func startDetached(exe, dir string, args []string, raw string, env []string) error {
	if !exists(exe) {
		return os.ErrNotExist
	}
	if os.Getenv("ACE8_FAKE_NEEDS_ADMIN") != "" && strings.EqualFold(filepath.Base(exe), "AceCombat8.exe") {
		return syscall.Errno(740) // "The requested operation requires elevation."
	}
	all := append(append([]string{}, args...), splitArgs(raw)...)
	record("started.txt", fmt.Sprintf("exe=%s dir=%s args=%q env=%q", exe, dir, all, env))
	if f := os.Getenv("ACE8_FAKE_RUNNING"); f != "" && strings.EqualFold(filepath.Base(exe), "steam.exe") {
		b, _ := os.ReadFile(f)
		os.WriteFile(f, append(b, []byte("\nsteam.exe\n")...), 0o644)
	}
	return nil
}

// startElevated: tests decline the permission prompt with ACE8_FAKE_DECLINE.
func startElevated(exe, dir string, args []string, raw string) error {
	if os.Getenv("ACE8_FAKE_DECLINE") != "" {
		return syscall.Errno(1223)
	}
	all := append(append([]string{}, args...), splitArgs(raw)...)
	record("started.txt", fmt.Sprintf("elevated exe=%s dir=%s args=%q", exe, dir, all))
	return nil
}

func runAsAdminSet(exe string) bool { return os.Getenv("ACE8_FAKE_NEEDS_ADMIN") != "" }

// steamActiveUser: tests sign Steam out with ACE8_FAKE_SIGNED_OUT.
func steamActiveUser() (uint32, bool) {
	if os.Getenv("ACE8_FAKE_SIGNED_OUT") != "" {
		return 0, true
	}
	return 1, true
}

func registrySteamPaths() []string { return nil }
func vcRuntimeInstalled() bool     { return os.Getenv("ACE8_FAKE_NO_VC") == "" }

func isCrossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }

func isSharingViolation(err error) bool { return errors.Is(err, syscall.EBUSY) }

func showFatal(msg string) { fmt.Fprintln(os.Stderr, msg) }
