// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"errors"
	"os"
	"path/filepath"
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
			for i := 0; i < 5000; i++ {
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
