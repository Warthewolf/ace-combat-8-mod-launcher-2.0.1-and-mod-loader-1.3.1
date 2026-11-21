//go:build !windows && !linuxapp

package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The game's window closed but its program kept running, so the launcher
// still says "Game running" and refuses to switch or play. End the game
// ends only the game's programs; then switching works again.
func TestEndStuckGame(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()
	setRunning(t, "AceCombat8.exe", "start_protected_game.exe", "steam.exe", "notepad.exe")

	if r := postJSON(t, srv, "switch", map[string]any{"to": "unmodded"}); r["ok"] != false {
		t.Fatalf("switching should wait for the game: %v", r)
	}
	r := postJSON(t, srv, "end-game", map[string]any{})
	if r["ok"] != true || fmt.Sprint(r["result"]) != "2" {
		t.Fatalf("end-game: %v", r)
	}
	if gameRunning() {
		t.Fatal("the game is still running")
	}
	left, _ := os.ReadFile(os.Getenv("ACE8_FAKE_RUNNING"))
	if !strings.Contains(string(left), "steam.exe") || !strings.Contains(string(left), "notepad.exe") {
		t.Fatalf("other programs must keep running: %q", left)
	}
	if r := postJSON(t, srv, "switch", map[string]any{"to": "unmodded"}); r["ok"] != true {
		t.Fatalf("switching after ending the game: %v", r)
	}

	// a game run as administrator: the launcher says how to end it by hand
	setRunning(t, "AceCombat8.exe")
	t.Setenv("ACE8_FAKE_END_REFUSED", "1")
	r = postJSON(t, srv, "end-game", map[string]any{})
	if r["ok"] != false || !strings.Contains(fmt.Sprint(r["error"]), "Task Manager") {
		t.Fatalf("refused end-game: %v", r)
	}
}
