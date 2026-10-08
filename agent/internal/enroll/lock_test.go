// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

func TestLockSucceedsWhenNoOtherEnrollIsRunning(t *testing.T) {
	certFile := filepath.Join(t.TempDir(), "tls.crt")
	unlock, err := enroll.Lock(certFile)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer unlock()
}

func TestLockRefusesASecondConcurrentEnroll(t *testing.T) {
	certFile := filepath.Join(t.TempDir(), "tls.crt")
	unlock, err := enroll.Lock(certFile)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	defer unlock()

	_, err = enroll.Lock(certFile)
	var eerr *enroll.Error
	if !errors.As(err, &eerr) {
		t.Fatalf("second Lock err = %v, want *enroll.Error", err)
	}
	if eerr.Class != enroll.ClassTemporary {
		t.Fatalf("class = %q, want temporary", eerr.Class)
	}
}

func TestUnlockLetsTheNextEnrollRun(t *testing.T) {
	certFile := filepath.Join(t.TempDir(), "tls.crt")
	unlock, err := enroll.Lock(certFile)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	unlock()

	unlock2, err := enroll.Lock(certFile)
	if err != nil {
		t.Fatalf("second Lock after unlock: %v", err)
	}
	unlock2()
}

func TestUnlockRemovesTheLockFile(t *testing.T) {
	certFile := filepath.Join(t.TempDir(), "tls.crt")
	unlock, err := enroll.Lock(certFile)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	unlock()
	if _, err := os.Stat(enroll.LockPath(certFile)); !os.IsNotExist(err) {
		t.Fatalf("lock file still present after unlock: err = %v", err)
	}
}

// В15: at most one holder at any moment, even while holders unlink the lock
// file on unlock (flock + unlink-on-unlock race: B opens path (inode1), A
// unlinks and closes, B flocks the now-unlinked inode1, C creates inode2
// and flocks it — two holders).
func TestLockNeverHasTwoHoldersAtOnce(t *testing.T) {
	cert := filepath.Join(t.TempDir(), "agent.pem")
	var holders, maxHolders atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20000; i++ {
				unlock, err := enroll.Lock(cert)
				if err != nil {
					continue
				}
				n := holders.Add(1)
				for m := maxHolders.Load(); n > m && !maxHolders.CompareAndSwap(m, n); m = maxHolders.Load() {
				}
				holders.Add(-1)
				unlock()
			}
		}()
	}
	wg.Wait()
	if got := maxHolders.Load(); got > 1 {
		t.Fatalf("%d holders at once, want at most 1", got)
	}
}

// The lock file's content is the holder's pid (so an operator inspecting a
// stuck lock can tell which process to look at).
func TestLockWritesThePIDToTheLockFile(t *testing.T) {
	certFile := filepath.Join(t.TempDir(), "tls.crt")
	unlock, err := enroll.Lock(certFile)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	defer unlock()

	got, err := os.ReadFile(enroll.LockPath(certFile))
	if err != nil {
		t.Fatalf("ReadFile(lock path): %v", err)
	}
	want := strconv.Itoa(os.Getpid()) + "\n"
	if string(got) != want {
		t.Fatalf("lock file content = %q, want %q", got, want)
	}
}

// openFDCount counts this process's open file descriptors via /proc, so a
// leak (a fd Lock/unlock should have closed but did not) is directly
// observable without depending on GC or finalizers.
func openFDCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("ReadDir(/proc/self/fd): %v", err)
	}
	return len(entries)
}

// Lock must close its file descriptor when a rival already holds the lock —
// otherwise every refused enroll leaks one fd forever.
func TestLockDoesNotLeakAFileDescriptorWhenARivalHoldsTheLock(t *testing.T) {
	certFile := filepath.Join(t.TempDir(), "tls.crt")
	unlock, err := enroll.Lock(certFile)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	defer unlock()

	before := openFDCount(t)
	for i := 0; i < 200; i++ {
		if _, err := enroll.Lock(certFile); err == nil {
			t.Fatal("second Lock unexpectedly succeeded while the first is held")
		}
	}
	after := openFDCount(t)
	if after > before {
		t.Fatalf("open fds grew from %d to %d after 200 refused Lock calls", before, after)
	}
}

// unlock must close its file descriptor — otherwise every successful
// enroll leaks one fd forever.
func TestLockDoesNotLeakAFileDescriptorAcrossManyLockUnlockCycles(t *testing.T) {
	certFile := filepath.Join(t.TempDir(), "tls.crt")
	// One warm-up cycle so the lock file already exists before we measure,
	// matching steady-state behavior.
	unlock, err := enroll.Lock(certFile)
	if err != nil {
		t.Fatalf("warm-up Lock: %v", err)
	}
	unlock()

	before := openFDCount(t)
	for i := 0; i < 200; i++ {
		unlock, err := enroll.Lock(certFile)
		if err != nil {
			t.Fatalf("Lock #%d: %v", i, err)
		}
		unlock()
	}
	after := openFDCount(t)
	if after > before {
		t.Fatalf("open fds grew from %d to %d after 200 Lock/unlock cycles", before, after)
	}
}

// F5: a lock file left behind by a crashed enroll (nobody holds the kernel
// flock on it any more) must not wedge every later enroll behind "an
// enrollment is already in progress" forever.
func TestLockIgnoresAStaleLockFileFromADeadProcess(t *testing.T) {
	certFile := filepath.Join(t.TempDir(), "tls.crt")
	// Nothing has this file open: write it directly, the way a crashed
	// process's O_CREATE would have left it, with a pid nothing now owns.
	if err := os.WriteFile(enroll.LockPath(certFile), []byte("999999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	unlock, err := enroll.Lock(certFile)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	unlock()
	if _, err := os.Stat(enroll.LockPath(certFile)); !os.IsNotExist(err) {
		t.Fatalf("lock file still present after unlock: err = %v", err)
	}
}

// The directory of the certificate belongs to the service user, the
// enrollment may run as root: no write through a link planted there.
func TestLockNeverWritesThroughALink(t *testing.T) {
	cases := map[string]func(dir, lock, victim string) error{
		"a symbolic link to a file": func(_, lock, victim string) error { return os.Symlink(victim, lock) },
		"a dangling symbolic link":  func(dir, lock, _ string) error { return os.Symlink(filepath.Join(dir, "created-by-root"), lock) },
		"a hard link":               func(_, lock, victim string) error { return os.Link(victim, lock) },
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) { assertLockRefusedBehindLink(t, plant) })
	}
}

func assertLockRefusedBehindLink(t *testing.T, plant func(dir, lock, victim string) error) {
	t.Helper()
	dir := t.TempDir()
	cert := filepath.Join(dir, "agent.pem")
	victim, lock := filepath.Join(dir, "victim"), enroll.LockPath(cert)
	if err := os.WriteFile(victim, []byte("root:x:0:0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := plant(dir, lock, victim); err != nil {
		t.Fatal(err)
	}
	unlock, err := enroll.Lock(cert)
	var eerr *enroll.Error
	if err == nil {
		unlock()
		t.Fatal("the lock was taken through the link")
	}
	if !errors.As(err, &eerr) || eerr.Class != enroll.ClassWrite {
		t.Fatalf("err = %v", err)
	}
	if data, _ := os.ReadFile(victim); string(data) != "root:x:0:0\n" {
		t.Fatalf("the victim was written: %q", data)
	}
	if _, err := os.Lstat(filepath.Join(dir, "created-by-root")); err == nil {
		t.Fatal("the target of the dangling link was created")
	}
}

// A lock file removed between Lock's open and its flock (a holder that just
// released) is refused, and the descriptor of that refused file is closed.
// The garbage collector is off so that a leaked descriptor is not closed by
// a finalizer before it is counted.
func TestLockOfAFileRemovedUnderneathDoesNotLeakAFileDescriptor(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	certFile := filepath.Join(t.TempDir(), "tls.crt")
	path := enroll.LockPath(certFile)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				_ = os.Remove(path)
			}
		}
	}()
	before := openFDCount(t)
	refused := 0
	for i := 0; i < 20000; i++ {
		unlock, err := enroll.Lock(certFile)
		if err != nil {
			refused++
			continue
		}
		unlock()
	}
	close(stop)
	<-done
	if after := openFDCount(t); after > before {
		t.Fatalf("open fds grew from %d to %d across %d refused Lock calls", before, after, refused)
	}
	t.Logf("%d of the Lock calls were refused", refused)
	if refused == 0 {
		t.Fatal("the race was not hit: the test proves nothing")
	}
}
