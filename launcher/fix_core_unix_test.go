//go:build !windows

package main

import "testing"

// srv-10: two launchers started at the same moment can't both hold the lock.
// (Windows uses a named mutex instead.)
func TestCoreInstanceLock(t *testing.T) {
	newGame(t)
	p := join(dataDir, "instance.lock")
	f, ok := tryLockFile(p)
	if !ok || f == nil {
		t.Fatal("first lock")
	}
	if f2, ok := tryLockFile(p); ok || f2 != nil {
		t.Fatal("a second launcher got the lock too")
	}
	f.Close()
	f3, ok := tryLockFile(p)
	if !ok || f3 == nil {
		t.Fatal("lock not free after the first launcher closed")
	}
	f3.Close()
}
