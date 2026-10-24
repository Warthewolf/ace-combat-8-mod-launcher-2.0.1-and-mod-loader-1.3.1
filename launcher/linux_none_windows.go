//go:build windows

package main

// The Windows program has none of the Linux parts (the Steam launch script and
// changing Steam's settings): they are only built into the Linux version.

import (
	"errors"
	"time"
)

type LinuxLaunch struct {
	Script  string `json:"script"`
	Set     bool   `json:"set"`
	Stale   bool   `json:"stale"`
	Current string `json:"current"`
}

var errLinuxOnly = errors.New("this is only needed on Linux")

func linuxLaunchState() *LinuxLaunch      { return nil }
func writeWrapper() error                 { return errLinuxOnly }
func setSteamLaunchOptions() (int, error) { return 0, errLinuxOnly }
func steamStartLogTime() time.Time        { return time.Time{} }
func steamConfigStamp() string            { return "" }
