// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"errors"
	"os"
	"path/filepath"
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
