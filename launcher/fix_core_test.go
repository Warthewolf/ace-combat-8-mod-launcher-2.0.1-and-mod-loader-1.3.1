package main

// Regression tests for the core fixes: the local server, start-up, settings,
// finding the game and Steam, and starting the game.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// upload sends files the way the window does: a "path" part, then its "file" part.
func upload(t *testing.T, srv *httptest.Server, files [][2]string, chunked bool) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range files {
		mw.WriteField("path", f[0])
		w, _ := mw.CreateFormFile("file", path2base(f[0]))
		w.Write([]byte(f[1]))
	}
	mw.Close()
	var body io.Reader = bytes.NewReader(buf.Bytes())
	if chunked {
		body = io.MultiReader(body) // no length known in advance
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/upload", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
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

func path2base(p string) string { return p[strings.LastIndex(p, "/")+1:] }

func uploadsLeft(t *testing.T) []string {
	ents, _ := os.ReadDir(uploadsDir())
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func decodeJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatalf("%v: %s", err, s)
	}
	return out
}

// srv-1: dropping the game's own Paks folder (or the whole game) is refused, not
// installed as a mod; so is adding a copy of it from a folder.
func TestCoreGamesOwnFilesAreNotAMod(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	write(t, join(g.Paks(), "pakchunk0-Windows.ucas"), "game cas")
	write(t, join(g.Paks(), "pakchunk0-Windows.pak"), "game pak")
	write(t, join(g.Paks(), "global.utoc"), "global toc")
	write(t, join(g.Paks(), "global.ucas"), "global cas")
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()

	// the whole Paks folder: stopped as soon as global.utoc arrives
	out := upload(t, srv, [][2]string{{"Paks/pakchunk0-Windows.utoc", "game"}, {"Paks/global.utoc", "global toc"}, {"Paks/global.ucas", "global cas"}}, false)
	if out["ok"] != false || !strings.Contains(fmt.Sprint(out["error"]), "Paks holds the game's own files (such as global.utoc)") {
		t.Fatalf("%v", out)
	}
	if left := uploadsLeft(t); len(left) != 0 {
		t.Fatalf("left in uploads: %v", left)
	}
	// only the pakchunk files: received, but not added
	out = upload(t, srv, [][2]string{{"Paks/pakchunk0-Windows.utoc", "game"}, {"Paks/pakchunk0-Windows.ucas", "game cas"}, {"Paks/pakchunk0-Windows.pak", "game pak"}}, false)
	if out["ok"] != true {
		t.Fatalf("%v", out)
	}
	add := decodeJSON(t, fixPost(t, srv, "add", `{"upload":"`+fmt.Sprint(out["result"])+`"}`))
	if add["ok"] != false || !strings.Contains(fmt.Sprint(add["error"]), "game's own files (such as pakchunk0-Windows.") {
		t.Fatalf("%v", add)
	}
	if lexists(join(g.loaderDir(), "Paks")) || lexists(join(g.loaderDir(), "loadorder.txt")) {
		t.Fatal("the game's files were installed as a mod")
	}
	// a copy of the whole game folder, added from where it is
	cp := join(t.TempDir(), "ACE COMBAT 8 backup")
	if err := copyTree(g.Root, cp); err != nil {
		t.Fatal(err)
	}
	if _, err := addFromPaths(g, []string{cp}, nil); err == nil || !strings.Contains(err.Error(), "game's own files") {
		t.Fatalf("err %v", err)
	}
	// a real mod with a pakchunk name is still a mod
	mod := join(t.TempDir(), "Hornet")
	container(t, mod, "pakchunk99-Windows")
	if res, err := addFromPaths(g, []string{mod}, nil); err != nil || !reflect.DeepEqual(res.Added, []string{"Hornet"}) {
		t.Fatalf("%+v %v", res, err)
	}
}

// srv-2 and srv-6: a drop is saved as it arrives, any number of files works, a
// failed drop leaves nothing behind, and a drop far too big is refused plainly.
func TestCoreUploadStreams(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()

	files := [][2]string{{"BigScript/Scripts/main.lua", "print(1)"}}
	for i := 0; i < 1200; i++ {
		files = append(files, [2]string{"BigScript/Scripts/lib/f" + strconv.Itoa(i) + ".lua", "x"})
	}
	out := upload(t, srv, files, false)
	if out["ok"] != true {
		t.Fatalf("1201 files: %v", out)
	}
	add := decodeJSON(t, fixPost(t, srv, "add", `{"upload":"`+fmt.Sprint(out["result"])+`"}`))
	if add["ok"] != true || !isFile(join(g.ue4ssRoot(), "Mods", "BigScript", "Scripts", "lib", "f1199.lua")) {
		t.Fatalf("%v", add["error"])
	}

	// a file and then a folder of the same name: the second can't be saved
	out = upload(t, srv, [][2]string{{"M/a", strings.Repeat("x", 1<<20)}, {"M/a/b", "y"}}, false)
	if out["ok"] != false || !strings.Contains(fmt.Sprint(out["error"]), "Could not save M/a/b") {
		t.Fatalf("%v", out)
	}
	if left := uploadsLeft(t); len(left) != 0 {
		t.Fatalf("left in uploads: %v", left)
	}

	// too big, with and without the size known in advance
	maxUpload = 4000
	defer func() { maxUpload = 16 << 30 }()
	for _, chunked := range []bool{false, true} {
		out = upload(t, srv, [][2]string{{"Huge/Huge.utoc", strings.Repeat("x", 3000)}, {"Huge/Huge.ucas", strings.Repeat("x", 3000)}}, chunked)
		if out["ok"] != false || !strings.Contains(fmt.Sprint(out["error"]), "far too big for a mod") {
			t.Fatalf("chunked %v: %v", chunked, out)
		}
		if left := uploadsLeft(t); len(left) != 0 {
			t.Fatalf("chunked %v: left in uploads: %v", chunked, left)
		}
	}
}

// srv-17: a folder whose name Windows can't store (possible on Linux) is
// added with "_" in place of the bad letters; a drop of only junk says so.
func TestCoreUploadOddNames(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()
	out := upload(t, srv, [][2]string{{"Skin: v2/Skin.utoc", "toc"}, {"Skin: v2/Skin.ucas", "cas"}, {"Skin: v2/Skin.pak", "pak"}, {"Skin: v2/.DS_Store", "junk"}}, false)
	if out["ok"] != true {
		t.Fatalf("%v", out)
	}
	add := decodeJSON(t, fixPost(t, srv, "add", `{"upload":"`+fmt.Sprint(out["result"])+`"}`))
	res, _ := add["result"].(map[string]any)
	if add["ok"] != true || fmt.Sprint(res["added"]) != "[Skin_ v2]" || !isFile(join(g.loaderDir(), "Skin_ v2", "Skin.ucas")) {
		t.Fatalf("%v", add)
	}
	out = upload(t, srv, [][2]string{{"Pics/.DS_Store", "junk"}, {"Pics/Thumbs.db", "junk"}}, false)
	if out["ok"] != false || !strings.Contains(fmt.Sprint(out["error"]), "Nothing in what you dropped can be used") {
		t.Fatalf("%v", out)
	}
	if left := uploadsLeft(t); len(left) != 0 {
		t.Fatalf("left in uploads: %v", left)
	}
}

// srv-4: while one request is looking for the game, the others don't wait,
// and choosing the game folder works at once.
func TestCoreChooseGameWhileSearching(t *testing.T) {
	g := newGame(t)
	settings.GameDir = ""
	lastNotFound = time.Time{}
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()
	detectMu.Lock() // a slow search (a sleeping drive) is running
	locked := true
	defer func() {
		if locked {
			detectMu.Unlock()
		}
	}()
	t.Setenv("ACE8_FAKE_PICK", g.Root)
	done := make(chan string, 1)
	go func() { done <- fixPost(t, srv, "pick-game", `{}`) }()
	select {
	case out := <-done:
		if !strings.Contains(out, `"ok":true`) || !samePath(getSettings().GameDir, g.Root) {
			t.Fatalf("%s", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Choose the game folder waited for the search")
	}
	start := time.Now()
	if _, ok := currentGame(); !ok || time.Since(start) > time.Second {
		t.Fatalf("game not known after choosing it: %v", ok)
	}
	detectMu.Unlock()
	locked = false
}

// srv-5: a game that takes minutes to appear is still noticed; the window can
// tell "still waiting" from "didn't start".
func TestCoreSlowGameStart(t *testing.T) {
	if launchWatchFor < 10*time.Minute {
		t.Fatalf("the game is only watched for %v", launchWatchFor)
	}
	g := newGame(t)
	defer func(d time.Duration) { launchWatchFor = d }(launchWatchFor)
	launchWatchFor = 4 * time.Second
	li, err := g.playWith(false)
	if err != nil {
		t.Fatal(err)
	}
	if L := currentLaunch(); !L.Waiting || L.SawGame || li.How != "steam" {
		t.Fatalf("%+v", L)
	}
	time.Sleep(1500 * time.Millisecond)
	setRunning(t, "AceCombat8.exe")
	waitFor(t, 3*time.Second, func() bool { L := currentLaunch(); return L.SawGame && !L.Waiting && !L.GaveUp })
	setRunning(t)
	waitFor(t, 4*time.Second, func() bool { return currentLaunch().GameEnded })

	// never appears: waiting ends, and the game didn't start
	launchWatchFor = time.Second
	if _, err := g.playWith(false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { L := currentLaunch(); return !L.Waiting })
	if L := currentLaunch(); L.SawGame || L.GameEnded || !L.GaveUp {
		t.Fatalf("%+v", L)
	}
}

func waitFor(t *testing.T, d time.Duration, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(d); !ok(); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatalf("still not so after %v: %+v", d, currentLaunch())
		}
	}
}

// srv-7: on Linux, a normal Steam folder left behind next to the Flatpak Steam
// in use doesn't win.
func TestCoreLinuxPicksTheSteamInUse(t *testing.T) {
	newGame(t)
	settings.GameDir = ""
	native, flatpak := join(t.TempDir(), "native"), join(t.TempDir(), "flatpak")
	for _, sd := range []string{native, flatpak} {
		os.MkdirAll(join(sd, "steamapps"), 0o755)
		write(t, join(sd, "userdata", "1", "config", "localconfig.vdf"), `"UserLocalConfigStore" {}`)
	}
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(join(native, "userdata", "1", "config", "localconfig.vdf"), old, old)
	write(t, join(flatpak, "steamapps", "appmanifest_2288340.acf"), `"AppState" { "installdir" "ACE COMBAT 8" }`)
	t.Setenv("ACE8_FAKE_STEAM", native+string(os.PathListSeparator)+flatpak)
	platform = "linux"
	defer func() { platform = "windows" }()
	if got := steamDir(); got != flatpak {
		t.Fatalf("the game's Steam: %s", got)
	}
	// neither has the game: the one whose settings were saved last
	os.Remove(join(flatpak, "steamapps", "appmanifest_2288340.acf"))
	now := time.Now()
	os.Chtimes(join(native, "userdata", "1", "config", "localconfig.vdf"), now, now)
	os.Chtimes(join(flatpak, "userdata", "1", "config", "localconfig.vdf"), old, old)
	if got := steamDir(); got != native {
		t.Fatalf("the newest settings: %s", got)
	}
	// Windows: the first one (the registry's) as before
	platform = "windows"
	os.Chtimes(join(flatpak, "userdata", "1", "config", "localconfig.vdf"), now.Add(time.Hour), now.Add(time.Hour))
	if got := steamDir(); got != native {
		t.Fatalf("windows: %s", got)
	}
}

// srv-8: saving settings works while the launcher is busy; settings that can't
// be saved are not used either.
func TestCoreSettingsWhileBusy(t *testing.T) {
	newGame(t)
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()
	if !busy.Try("switching") {
		t.Fatal("busy")
	}
	out := fixPost(t, srv, "settings", `{"welcomeDone":true,"extraArgs":" -dx12 "}`)
	busy.Done()
	if !strings.Contains(out, `"ok":true`) || !strings.Contains(out, `"state"`) {
		t.Fatalf("%s", out)
	}
	if s := getSettings(); !s.WelcomeDone || s.ExtraArgs != "-dx12" {
		t.Fatalf("%+v", s)
	}
	if b, _ := os.ReadFile(settingsPath()); !strings.Contains(string(b), `"welcomeDone": true`) {
		t.Fatalf("not saved: %s", b)
	}
	// the folder can't be written: nothing changes
	before := getSettings()
	file := join(t.TempDir(), "a file")
	write(t, file, "")
	keep := dataDir
	dataDir = join(file, "data")
	err := updateSettings(func(s *Settings) { s.ExtraArgs, s.StorageDir = "-lost", "X:\\lost" })
	dataDir = keep
	if err == nil || !reflect.DeepEqual(getSettings(), before) {
		t.Fatalf("err %v, settings %+v", err, getSettings())
	}
}

// srv-10 and srv-13: on Linux the launcher's files are only for this account;
// a launcher leaving removes only its own instance.json. (The lock that keeps
// a second launcher from starting: fix_core_unix_test.go.)
func TestCoreInstanceFiles(t *testing.T) {
	newGame(t)
	dataDir = join(t.TempDir(), "data")
	os.MkdirAll(dataDir, 0o755)
	prepareDataDir()
	token = "tok-" + strconv.Itoa(os.Getpid())
	defer func() { token = "" }()
	writeInstanceFile(1234)
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(dataDir); st.Mode().Perm() != 0o700 {
			t.Errorf("data folder %v", st.Mode().Perm())
		}
		if st, _ := os.Stat(instancePath()); st.Mode().Perm() != 0o600 {
			t.Errorf("instance.json %v", st.Mode().Perm())
		}
	}
	var in instanceInfo
	b, _ := os.ReadFile(instancePath())
	if json.Unmarshal(b, &in) != nil || in.Port != 1234 || in.Token != token || in.PID != os.Getpid() {
		t.Fatalf("%s", b)
	}
	// another launcher took over: its file stays
	other, _ := json.Marshal(instanceInfo{Port: 99, Token: "theirs", PID: os.Getpid() + 1})
	os.WriteFile(instancePath(), other, 0o600)
	removeInstanceFile()
	if !isFile(instancePath()) {
		t.Fatal("removed the other launcher's instance.json")
	}
	writeInstanceFile(1234)
	removeInstanceFile()
	if lexists(instancePath()) {
		t.Fatal("own instance.json left")
	}
}

// srv-11: a launcher that says it is busy is never stopped.
func TestCoreReplaceLeavesABusyLauncher(t *testing.T) {
	newGame(t)
	sleeper := exec.Command("sleep", "30")
	if err := sleeper.Start(); err != nil {
		t.Skip("no sleep command:", err)
	}
	defer sleeper.Process.Kill()
	quits := 0
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/hello":
			w.Write([]byte(`{"ok":true,"version":"2.0.4","build":"2.0.4-old","busy":""}`))
		case "/api/quit":
			quits++
			w.Write([]byte(`{"ok":false,"error":"busy: switching"}`))
		}
	}))
	defer old.Close()
	u, _ := url.Parse(old.URL)
	_, port, _ := net.SplitHostPort(u.Host)
	pn, _ := strconv.Atoi(port)
	in, _ := json.Marshal(instanceInfo{Port: pn, Token: "t", PID: sleeper.Process.Pid})
	os.WriteFile(instancePath(), in, 0o600)
	ri := findRunningInstance()
	if ri.URL == "" || ri.Version != "2.0.4" {
		t.Fatalf("%+v", ri)
	}
	start := time.Now()
	if ri.replace() {
		t.Fatal("replaced a busy launcher")
	}
	if quits != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("quits %d, took %v", quits, time.Since(start))
	}
	if err := sleeper.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("the busy launcher was stopped: %v", err)
	}
}

// srv-14: a message that starts with a non-English letter stays readable.
func TestCoreMessagesKeepLetters(t *testing.T) {
	for in, want := range map[string]string{
		"機体スキン.7z is a .7z archive":    "機体スキン.7z is a .7z archive.",
		"émile skin could not be read": "Émile skin could not be read.",
		"no mod (here)":                "No mod (here)",
		"":                             "",
	} {
		if got := sentence(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	rec := httptest.NewRecorder()
	writeErr(rec, fmt.Errorf("émile is gone"))
	if !strings.Contains(rec.Body.String(), `"error":"Émile is gone."`) {
		t.Fatalf("%s", rec.Body)
	}
}

// srv-16: a game folder typed without its full path is saved in full, and
// such paths saved by an older launcher are made full when read.
func TestCoreTypedGameFolderIsSavedInFull(t *testing.T) {
	g := newGame(t)
	base := filepath.Dir(filepath.Dir(filepath.Dir(g.Root)))
	t.Chdir(base)
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()
	out := fixPost(t, srv, "game", `{"path":"steamapps/common/ACE COMBAT 8"}`)
	if !strings.Contains(out, `"ok":true`) || getSettings().GameDir != g.Root {
		t.Fatalf("%s; saved %q", out, getSettings().GameDir)
	}
	write(t, settingsPath(), `{"gameDir":"steamapps/common/ACE COMBAT 8","storageDir":"AC8 storage","storageGame":"steamapps/common/ACE COMBAT 8"}`)
	loadSettings()
	if s := getSettings(); s.GameDir != g.Root || s.StorageGame != g.Root || s.StorageDir != join(base, "AC8 storage") {
		t.Fatalf("%+v", s)
	}
}

// srv-18: the window's own files have fixed types, whatever Windows' registry says.
func TestCoreWindowFileTypes(t *testing.T) {
	newGame(t)
	mime.AddExtensionType(".css", "text/plain") // what a program could register
	defer mime.AddExtensionType(".css", "text/css; charset=utf-8")
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()
	for f, want := range map[string]string{"app.css": "text/css", "app.js": "text/javascript", "": "text/html", "icon.png": "image/png"} {
		resp, err := http.Get(srv.URL + "/" + f)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, want) || resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: %q", f, ct)
		}
	}
}

// srv-19: a game folder on a drive that is not connected is kept; another copy
// is offered, not taken.
func TestCoreUnpluggedGameFolderIsKept(t *testing.T) {
	g := newGame(t)
	base := filepath.Dir(filepath.Dir(filepath.Dir(g.Root)))
	unplugged := join(t.TempDir(), "E", "SteamLibrary", "steamapps", "common", "ACE COMBAT 8")
	settings.GameDir = unplugged
	t.Setenv("ACE8_FAKE_STEAM", base) // its library has the other copy
	lastNotFound = time.Time{}
	if _, ok := currentGame(); ok || getSettings().GameDir != unplugged {
		t.Fatalf("replaced by the other copy: %s", getSettings().GameDir)
	}
	st := buildState()
	if st.Game.Found || st.Game.Missing != unplugged || st.Game.Elsewhere != g.Root {
		t.Fatalf("%+v", st.Game)
	}
	if err := errNoGame(); !strings.Contains(err.Error(), "("+unplugged+") can't be found. Connect the drive") {
		t.Fatal(err)
	}
	// the player takes the other copy
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()
	body, _ := json.Marshal(map[string]string{"path": st.Game.Elsewhere})
	if out := fixPost(t, srv, "game", string(body)); !strings.Contains(out, `"ok":true`) {
		t.Fatal(out)
	}
	if g2, ok := currentGame(); !ok || g2.Root != g.Root {
		t.Fatalf("%v %s", ok, g2.Root)
	}
	// a game folder whose library is still there (Steam moved or uninstalled
	// the game) is still found again by itself
	gone := join(base, "steamapps", "common", "ACE COMBAT 8 (old)")
	settings.GameDir = gone
	lastNotFound = time.Time{}
	if g2, ok := currentGame(); !ok || g2.Root != g.Root || getSettings().GameDir != g.Root {
		t.Fatalf("%v %s", ok, g2.Root)
	}
}

// srv-9: when the launcher starts Steam and the signed-in account in the
// registry may be left over, Play waits for Steam's helper first.
func TestCoreStaleSteamSignIn(t *testing.T) {
	newGame(t)
	steamHome := t.TempDir()
	write(t, join(steamHome, "steam.exe"), "steam")
	t.Setenv("ACE8_FAKE_STEAM", steamHome)
	t.Setenv("ACE8_FAKE_NO_STEAM", "1")
	t.Setenv("ACE8_FAKE_NO_WEBHELPER", "1")
	defer func(d time.Duration) { steamHelperWait = d }(steamHelperWait)
	steamHelperWait = 1500 * time.Millisecond
	start := time.Now()
	if err := ensureSteam(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 3*time.Second+steamHelperWait {
		t.Fatalf("did not wait for Steam's helper: %v", d)
	}
}

// Part B 1-3: problems that stop a mod from loading are warnings, said plainly;
// the add and setup errors say what to do.
func TestCoreAddWarningsAndMessages(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	logic := join(t.TempDir(), "Logic.zip")
	makeZip(t, logic, map[string]string{"LogicMods/MyLogic/MyLogic.pak": "p", "LogicMods/MyLogic/MyLogic.utoc": "t", "LogicMods/MyLogic/MyLogic.ucas": "c"})
	nocas := join(t.TempDir(), "Half.zip")
	makeZip(t, nocas, map[string]string{"Half/Half.utoc": "t", "Half/Half.pak": "p"})
	res, err := addFromPaths(g, []string{logic, nocas}, nil)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(res.Warnings, " | ")
	if len(res.Warnings) != 2 || !strings.Contains(all, "MyLogic won't load yet: LogicMods mods need UE4SS's BPModLoaderMod") ||
		!strings.Contains(all, "Part of Half is missing from the download (Half.ucas), so it won't load. Download it again") ||
		strings.Contains(strings.Join(res.Notes, " "), "BPModLoaderMod") {
		t.Fatalf("%+v", res)
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), `"warnings":[`) {
		t.Fatalf("%s", b)
	}
	b, _ = json.Marshal(AddResult{})
	if strings.Contains(string(b), "warnings") {
		t.Fatalf("%s", b)
	}

	pics := join(t.TempDir(), "Preview Pics.zip")
	makeZip(t, pics, map[string]string{"preview.png": "png", "readme.txt": "hi"})
	_, err = addFromPaths(g, []string{pics}, nil)
	if err == nil || sentence(err.Error()) != "No mod was found in Preview Pics. Check that it is the mod's main download, not a preview or a readme. (A mod has .utoc and .ucas files, a file ending in _P.pak, or a Scripts\\main.lua file.)" {
		t.Fatalf("%v", err)
	}

	g2 := newGame(t)
	if _, err := g2.setup(); err != errNoBundle || !strings.HasSuffix(err.Error(), "next to the launcher's folder, then press Set up modding on the Play page") {
		t.Fatalf("%v", err)
	}
}

// Part B 4: the window notices the Mod Loader download put next to the launcher.
func TestCoreFingerprintSeesModLoaderDownload(t *testing.T) {
	g := newGame(t)
	resetBundle := func() {
		bundleMu.Lock()
		bundleAt = time.Time{}
		bundleMu.Unlock()
	}
	resetBundle()
	before := buildStatus().Fingerprint
	dl := join(filepath.Dir(exeDir), "War's Mod Loader 1.3.1")
	write(t, join(dl, "Game", "Binaries", "Win64", "dwmapi.dll"), "dwm")
	write(t, join(dl, "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod", "dlls", "main.dll"), "MZ..."+utf16le("1.3.1-ac8-1.1.2.0-ue5.4")+"...")
	resetBundle()
	with := buildStatus().Fingerprint
	if with == before || !g.setupStatus().BundleFound {
		t.Fatalf("fingerprint did not change: %s", with)
	}
	os.RemoveAll(dl)
	resetBundle()
	if buildStatus().Fingerprint == with {
		t.Fatal("fingerprint did not change when the download went")
	}
}

// Part B 6: an old copy left by an update cut short is cleaned up before Play,
// before a switch and at start-up, not only at the next add.
func TestCoreUnfinishedUpdatesAreSwept(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	srv := httptest.NewServer(newMux(0))
	defer srv.Close()
	ld := g.loaderDir()
	// the new copy is all in: the old one goes
	container(t, join(ld, "Hornet"), "Hornet")
	container(t, join(ld, "Hornet"+updatingSuffix), "Hornet")
	if out := fixPost(t, srv, "play", `{}`); !strings.Contains(out, `"ok":true`) {
		t.Fatal(out)
	}
	if lexists(join(ld, "Hornet"+updatingSuffix)) || !isDir(join(ld, "Hornet")) {
		t.Fatalf("before Play: %v", names(ld))
	}
	// the new copy only got in part of the way: the old one comes back, and it is what is switched off
	write(t, join(ld, "Hornet"+updatingSuffix, "Hornet.utoc"), "old toc")
	write(t, join(ld, "Hornet"+incompleteSuffix), "")
	if out := fixPost(t, srv, "switch", `{"to":"unmodded"}`); !strings.Contains(out, `"ok":true`) {
		t.Fatal(out)
	}
	off := join(g.OffDir(), "Game", "Binaries", "Win64", "ue4ss", "Mods", "IoStoreLoaderMod")
	if readS(t, join(off, "Hornet", "Hornet.utoc")) != "old toc" || lexists(join(off, "Hornet"+updatingSuffix)) || lexists(join(off, "Hornet"+incompleteSuffix)) {
		t.Fatalf("before a switch: %v", names(off))
	}
	if _, err := g.switchTo(modeModded); err != nil {
		t.Fatal(err)
	}
	// at start-up
	container(t, join(ld, "Su57"), "Su57")
	container(t, join(ld, "Su57"+updatingSuffix), "Su57")
	tidyAtStart()
	if lexists(join(ld, "Su57"+updatingSuffix)) || !isDir(join(ld, "Su57")) || busy.Running() {
		t.Fatalf("at start-up: %v", names(ld))
	}
}

// Part B 7: a version picked from a download is named after the download, and
// adding it again (or from a newer download) updates it in place.
func TestCoreVersionNamedAfterDownload(t *testing.T) {
	g := newGame(t)
	installModding(t, g)
	dl := func(name, red string) string {
		d := join(t.TempDir(), name)
		write(t, join(d, "Red", "Livery.utoc"), "toc")
		write(t, join(d, "Red", "Livery.ucas"), red)
		write(t, join(d, "Red", "Livery.pak"), "p")
		write(t, join(d, "Blue", "Livery.utoc"), "toc")
		write(t, join(d, "Blue", "Livery.ucas"), "blue")
		write(t, join(d, "Blue", "Livery.pak"), "p")
		return d
	}
	d := dl("TwoVersions", "red")
	res, err := addFromPaths(g, []string{d}, nil)
	if err != nil || len(res.Choices) != 2 || res.Choices[1].Name != "Red" {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = addFromPaths(g, []string{d}, []string{"TwoVersions|pak:red"})
	if err != nil || !reflect.DeepEqual(res.Added, []string{"TwoVersions - Red"}) {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = addFromPaths(g, []string{d}, []string{"TwoVersions|pak:red"})
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"TwoVersions - Red"}) || len(res.Added) != 0 {
		t.Fatalf("again: %+v %v", res, err)
	}
	res, err = addFromPaths(g, []string{dl("TwoVersions v2", "red v2")}, []string{"TwoVersions v2|pak:red"})
	if err != nil || !reflect.DeepEqual(res.Updated, []string{"TwoVersions - Red"}) {
		t.Fatalf("newer download: %+v %v", res, err)
	}
	if readS(t, join(g.loaderDir(), "TwoVersions - Red", "Livery.ucas")) != "red v2" {
		t.Fatal("not updated")
	}
	mods, _ := scanPakMods(g.loaderDir(), g.tildeModsDir())
	if got := keys(mods); !reflect.DeepEqual(got, []string{"TwoVersions - Red"}) {
		t.Fatalf("mods %q", got)
	}
	// the other version replaces it
	res, err = addFromPaths(g, []string{d}, []string{"TwoVersions|pak:blue"})
	if err != nil || !strings.Contains(strings.Join(res.Notes, " "), "The older copy in TwoVersions - Red was replaced") {
		t.Fatalf("%+v %v", res, err)
	}
	mods, _ = scanPakMods(g.loaderDir(), g.tildeModsDir())
	if got := keys(mods); !reflect.DeepEqual(got, []string{"TwoVersions - Blue"}) {
		t.Fatalf("mods %q", got)
	}
}
