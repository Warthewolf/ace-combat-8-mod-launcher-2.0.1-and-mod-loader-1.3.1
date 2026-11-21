package main

import (
	"os"
	"testing"
	"time"
)

// UE4SS starts the Lua mods a few seconds after the Mod Loader wrote its log.
// The window must notice UE4SS.log growing after Play, or it keeps showing
// every script mod as "Not started" for the whole game.
func TestScriptLogWatchedAfterPlay(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	log := join(g.ue4ssRoot(), "UE4SS.log")
	write(t, log, "Setting up mods...\r\n")
	old := time.Now().Add(-time.Minute)
	os.Chtimes(log, old, old)

	if _, err := g.playWith(false); err != nil {
		t.Fatal(err)
	}
	before := buildStatus().Fingerprint
	// the Lua mods start a little later
	write(t, log, "Setting up mods...\r\nMod 'PartsOverdrive' has enabled.txt, starting mod.\r\n")
	if buildStatus().Fingerprint == before {
		t.Fatal("UE4SS.log grew after Play, but the window is not told to read it again")
	}
	if sr := buildState().ScriptRun; sr == nil || !sr.Started["partsoverdrive"] {
		t.Fatalf("PartsOverdrive should show as started: %+v", sr)
	}

	// long after Play, a mod writing to the log no longer reloads the window
	defer func(d time.Duration) { scriptStartWatch = d }(scriptStartWatch)
	scriptStartWatch = 0
	before = buildStatus().Fingerprint
	write(t, log, "Setting up mods...\r\nMod 'PartsOverdrive' has enabled.txt, starting mod.\r\n[Lua] [PartsOverdrive] still here\r\n")
	later := time.Now().Add(5 * time.Second)
	os.Chtimes(log, later, later)
	if buildStatus().Fingerprint != before {
		t.Fatal("UE4SS.log is still followed long after Play")
	}
}
