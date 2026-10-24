package main

// Each open window gives itself a name and says "bye" when it closes, so the
// program can leave a few seconds after its last window has closed instead of
// lingering in the background. (Same rules as ACE8 Skin Studio.)

import (
	"os"
	"sync"
	"time"
)

const (
	leaveAfterLastWindow = 3 * time.Second
	leaveWhenSilent      = 100 * time.Second
	sleepGap             = 20 * time.Second
)

type presenceState struct {
	mu        sync.Mutex
	windows   map[string]time.Time
	hadWindow bool
	lastHeard time.Time
}

var presence = &presenceState{windows: map[string]time.Time{}, lastHeard: time.Now()}

func (p *presenceState) heard(id string) {
	now := time.Now()
	p.mu.Lock()
	p.lastHeard = now
	if id != "" && len(id) <= 64 {
		p.windows[id] = now
		p.hadWindow = true
	}
	p.mu.Unlock()
}

func (p *presenceState) bye(id string) {
	p.mu.Lock()
	delete(p.windows, id)
	p.lastHeard = time.Now()
	p.mu.Unlock()
}

func (p *presenceState) shouldLeave(now time.Time, woke bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if woke {
		p.lastHeard = now
		for id := range p.windows {
			p.windows[id] = now
		}
		return false
	}
	for id, seen := range p.windows {
		if now.Sub(seen) > leaveWhenSilent {
			delete(p.windows, id)
		}
	}
	silent := now.Sub(p.lastHeard)
	if p.hadWindow && len(p.windows) == 0 && silent > leaveAfterLastWindow {
		return true
	}
	return silent > leaveWhenSilent
}

func watchdog() {
	last := time.Now()
	for {
		time.Sleep(time.Second)
		now := time.Now()
		woke := now.Sub(last) > sleepGap
		last = now
		if busy.Running() {
			continue // a switch or an install carries on to the end even with the window closed
		}
		if presence.shouldLeave(now, woke) {
			logf("window closed; exiting")
			os.Remove(instancePath())
			os.Exit(0)
		}
	}
}

// busy guards everything that changes files, so two of them never run at once.
type busyLock struct {
	mu      sync.Mutex
	running bool
	what    string
}

var busy = &busyLock{}

func (b *busyLock) Try(what string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return false
	}
	b.running, b.what = true, what
	return true
}

func (b *busyLock) Done() {
	b.mu.Lock()
	b.running, b.what = false, ""
	b.mu.Unlock()
}

func (b *busyLock) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}

func (b *busyLock) What() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.what
}
