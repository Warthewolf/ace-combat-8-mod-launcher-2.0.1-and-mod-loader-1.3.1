//go:build !windows && !linuxapp

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func postJSON(t *testing.T, srv *httptest.Server, path string, body any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", srv.URL+"/api/"+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Token", token)
	req.Host = "127.0.0.1:0"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A player presses Add a mod…, then drags the zip from the file window onto the
// launcher instead of pressing Open. The open file window must not make the
// launcher "busy": the dropped mod is added. A second file window is refused.
func TestDropWhileFileWindowIsOpen(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()

	pickOpen = make(chan struct{})
	defer func() { pickOpen = nil }()
	picked := make(chan map[string]any, 1)
	go func() { picked <- postJSON(t, srv, "pick-mods", map[string]any{"folder": false}) }()
	time.Sleep(200 * time.Millisecond) // the file window is now open

	up := upload(t, srv, [][2]string{{"Freecam/Freecam_P.utoc", "toc"}, {"Freecam/Freecam_P.ucas", "cas"}}, false)
	if up["ok"] != true {
		t.Fatalf("the drop was refused while the file window was open: %v", up)
	}
	add := postJSON(t, srv, "add", map[string]any{"upload": up["result"]})
	if add["ok"] != true || !strings.Contains(fmt.Sprint(add["result"]), "Freecam") {
		t.Fatalf("the dropped mod was not added: %v", add)
	}
	if again := postJSON(t, srv, "pick-mods", map[string]any{}); again["ok"] != false || !strings.Contains(fmt.Sprint(again["error"]), "open already") {
		t.Fatalf("a second file window should be refused: %v", again)
	}

	close(pickOpen) // the player presses Cancel
	select {
	case r := <-picked:
		if r["ok"] != true {
			t.Fatalf("the file window's answer: %v", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the file window never answered")
	}
	if busy.Running() {
		t.Fatal("the launcher is still busy")
	}
}
