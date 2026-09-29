// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

func TestErrorTextNamesTheClassAndReason(t *testing.T) {
	err := &enroll.Error{Class: enroll.ClassTokenRefused, Reason: "TOKEN_USED"}
	if !strings.Contains(err.Error(), string(enroll.ClassTokenRefused)) || !strings.Contains(err.Error(), "TOKEN_USED") {
		t.Fatalf("Error() = %q", err.Error())
	}
}

func TestErrorTextIncludesTheWrappedCauseAndUnwraps(t *testing.T) {
	// CheckWritable on a missing directory produces an *enroll.Error that
	// wraps the underlying os error (write.go sets both msg and err).
	base := t.TempDir()
	files := enroll.Files{
		KeyFile:  filepath.Join(base, "missing", "tls.key"),
		CertFile: filepath.Join(base, "tls.crt"),
		CAFile:   filepath.Join(base, "ca.crt"),
	}
	err := enroll.CheckWritable(files)
	var eerr *enroll.Error
	if !errors.As(err, &eerr) {
		t.Fatalf("err = %v, want *enroll.Error", err)
	}
	if !strings.Contains(eerr.Error(), string(enroll.ClassWrite)) {
		t.Fatalf("Error() = %q, want the class in it", eerr.Error())
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("errors.Is did not see through to the wrapped os error: %v", err)
	}
	// The message set by CheckWritable ("... is not writable") must appear
	// in Error() text, not just the class.
	if !strings.Contains(eerr.Error(), "is not writable") {
		t.Fatalf("Error() = %q, want the msg text in it too", eerr.Error())
	}
	// The wrapped os error's own text must appear in Error() as well, not
	// just be reachable via Unwrap.
	wrapped := errors.Unwrap(err)
	if wrapped == nil {
		t.Fatal("want a non-nil wrapped error")
	}
	if !strings.Contains(eerr.Error(), wrapped.Error()) {
		t.Fatalf("Error() = %q, want it to contain the wrapped error text %q", eerr.Error(), wrapped.Error())
	}
}
