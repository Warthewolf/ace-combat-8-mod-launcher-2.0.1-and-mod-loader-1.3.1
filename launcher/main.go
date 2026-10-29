// ACE8 Mod Launcher: one window for playing Ace Combat 8 with or without mods.
//
// The program is a small local web server; its window is an Edge "app" window
// (the same approach as ACE8 Skin Studio). Everything it does is on this PC:
// it moves the game's mod files between the game folder and a storage folder
// next to it, keeps the Mod Loader's load order, and starts the game.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	appName    = "ACE8 Mod Launcher"
	appVersion = "2.0.2"
	steamAppID = "2288340"
	contact    = "Any issues please contact @voidcallsreaper on Discord"
)

var (
	flagPort   = flag.Int("port", 0, "port to listen on (0 = any free port)")
	flagNoOpen = flag.Bool("no-open", false, "do not open the window")
	flagData   = flag.String("data", "", "folder for settings and logs")
	flagToken  = flag.String("token", "", "fixed access token (tests)")
	flagStay   = flag.Bool("stay", false, "keep running when no window is open (tests)")
	flagExeDir = flag.String("exedir", "", "folder the program counts as its own (tests)")
)

var (
	dataDir string
	exeDir  string
	token   string
	baseURL string

	logMu   sync.Mutex
	logFile *os.File
)

func logf(format string, a ...any) {
	line := time.Now().Format("2006-01-02 15:04:05 ") + fmt.Sprintf(format, a...)
	logMu.Lock()
	defer logMu.Unlock()
	if logFile != nil {
		logFile.WriteString(line + "\r\n")
	}
}

func openLog() {
	p := filepath.Join(dataDir, "launcher.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 1<<20 {
		os.Remove(p + ".old")
		os.Rename(p, p+".old")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		logFile = f
	}
}

func defaultDataDir() string {
	if d := platformDataDir(); d != "" {
		return d
	}
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return filepath.Join(d, appName)
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".ace8modlauncher")
	}
	return "."
}

func newToken() string {
	b := make([]byte, 18)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// instance.json lets a second start of the program bring up the window of the first one.
type instanceInfo struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func instancePath() string { return filepath.Join(dataDir, "instance.json") }

type runningInstance struct {
	URL     string // its window, "" when no launcher is running
	Version string // "" for launchers before 2.0.1
	Build   string // which exe exactly
	Busy    string
	info    instanceInfo
}

func findRunningInstance() runningInstance {
	b, err := os.ReadFile(instancePath())
	if err != nil {
		return runningInstance{}
	}
	var in instanceInfo
	if json.Unmarshal(b, &in) != nil || in.Port == 0 || in.Token == "" {
		return runningInstance{}
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", in.Port)
	c := http.Client{Timeout: 1500 * time.Millisecond}
	req, _ := http.NewRequest("GET", url+"api/hello", nil)
	req.Header.Set("X-Token", in.Token)
	resp, err := c.Do(req)
	if err != nil {
		return runningInstance{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return runningInstance{}
	}
	var hello struct{ Version, Build, Busy string }
	json.NewDecoder(resp.Body).Decode(&hello)
	return runningInstance{URL: url + "?t=" + in.Token, Version: hello.Version, Build: hello.Build, Busy: hello.Busy, info: in}
}

// replace closes a running launcher of another version, so this one takes over.
// Without this, starting a new launcher while an old one was still open showed
// the old one's window.
func (ri runningInstance) replace() bool {
	if ri.Busy != "" {
		return false // a switch or an install is running: let it finish
	}
	base := strings.TrimSuffix(ri.URL, "?t="+ri.info.Token)
	c := http.Client{Timeout: 1500 * time.Millisecond}
	req, _ := http.NewRequest("POST", base+"api/quit", nil)
	req.Header.Set("X-Token", ri.info.Token)
	if resp, err := c.Do(req); err == nil {
		resp.Body.Close()
	}
	for i := 0; i < 20; i++ {
		time.Sleep(150 * time.Millisecond)
		if findRunningInstance().URL == "" {
			return true
		}
	}
	// launchers before 2.0.1 have no way to be asked: stop the process
	if p, err := os.FindProcess(ri.info.PID); err == nil && ri.info.PID > 0 && ri.info.PID != os.Getpid() {
		p.Kill()
	}
	for i := 0; i < 20; i++ {
		time.Sleep(150 * time.Millisecond)
		if findRunningInstance().URL == "" {
			return true
		}
	}
	return false
}

// buildID tells two builds of the launcher apart, even with the same version number.
var buildID string

func computeBuildID() string {
	p, err := os.Executable()
	if err != nil {
		return appVersion
	}
	f, err := os.Open(p)
	if err != nil {
		return appVersion
	}
	defer f.Close()
	h := sha256.New()
	io.Copy(h, f)
	return appVersion + "-" + hex.EncodeToString(h.Sum(nil))[:12]
}

func main() {
	flag.Parse()
	buildID = computeBuildID()

	dataDir = *flagData
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	os.MkdirAll(dataDir, 0o755)
	openLog()

	exeDir = *flagExeDir
	if exeDir == "" {
		if p, err := os.Executable(); err == nil {
			exeDir = filepath.Dir(p)
		}
	}

	if *flagToken == "" {
		if ri := findRunningInstance(); ri.URL != "" {
			if ri.Build == buildID || !ri.replace() {
				logf("already running (version %q); showing its window", ri.Version)
				openURL(ri.URL, true)
				return
			}
			logf("closed the running launcher (version %q) to start %s", ri.Version, appVersion)
		}
	}

	loadSettings()
	token = *flagToken
	if token == "" {
		token = newToken()
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *flagPort))
	if err != nil {
		logf("cannot listen: %v", err)
		showFatal("ACE8 Mod Launcher could not start: " + err.Error())
		return
	}
	port := ln.Addr().(*net.TCPAddr).Port
	baseURL = fmt.Sprintf("http://127.0.0.1:%d/", port)

	in, _ := json.Marshal(instanceInfo{Port: port, Token: token, PID: os.Getpid()})
	os.WriteFile(instancePath(), in, 0o644)

	logf("%s %s started on %s, data=%s, program folder=%s", appName, appVersion, platform, dataDir, exeDir)
	platformStart()
	presence.heard("")
	if !*flagNoOpen {
		if err := openURL(baseURL+"?t="+token, true); err != nil {
			logf("could not open the window: %v", err)
		}
	}
	if !*flagStay {
		go watchdog()
	}

	srv := &http.Server{Handler: newMux(port), ReadHeaderTimeout: 10 * time.Second}
	if err := srv.Serve(ln); err != nil && !strings.Contains(err.Error(), "closed") {
		logf("server stopped: %v", err)
	}
}
