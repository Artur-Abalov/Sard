// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
)

// docs/specs/agent/agent-certificate-renewal.feature, P25.

func TestTheStateIsAJSONObjectWithTheFieldsOfP25(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cert-renewal.json")
	st := certrenew.State{Serial: "abc", Failures: 3, LastError: "Unavailable INTERNAL_RETRYABLE", LastAttempt: t0, NextAttempt: t0.Add(time.Minute)}
	if err := certrenew.WriteState(certpair.OSFS{}, path, st); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	want := `{"serial":"abc","failures":3,"last_error":"Unavailable INTERNAL_RETRYABLE","last_attempt":"2026-10-15T12:00:00Z","next_attempt":"2026-10-15T12:01:00Z"}`
	info, _ := os.Stat(path)
	if string(data) != want || info.Mode().Perm() != 0o600 {
		t.Fatalf("state = %s, mode = %v", data, info.Mode())
	}
}

func TestTheStateIsReadBack(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cert-renewal.json")
	want := certrenew.State{Serial: "abc", Failures: 3, LastError: "x", LastAttempt: t0, NextAttempt: t0.Add(time.Minute)}
	if err := certrenew.WriteState(certpair.OSFS{}, path, want); err != nil {
		t.Fatal(err)
	}
	got, ok := certrenew.ReadState(os.ReadFile, path)
	if !ok || got.Serial != want.Serial || got.Failures != 3 || !got.NextAttempt.Equal(want.NextAttempt) || !got.LastAttempt.Equal(t0) {
		t.Fatalf("state = %+v, ok = %v", got, ok)
	}
}

// Scenario: Файл состояния, не относящийся к сертификату, не учитывается
func TestAMissingUnreadableOrBrokenStateIsAsIfThereWasNone(t *testing.T) {
	t.Parallel()
	for name, read := range map[string]func(string) ([]byte, error){
		"missing":    func(string) ([]byte, error) { return nil, os.ErrNotExist },
		"unreadable": func(string) ([]byte, error) { return nil, os.ErrPermission },
		"not json":   func(string) ([]byte, error) { return []byte("{"), nil },
		"no serial":  func(string) ([]byte, error) { return []byte(`{"failures":3}`), nil },
	} {
		if st, ok := certrenew.ReadState(read, "x"); ok {
			t.Errorf("%s: state = %+v", name, st)
		}
	}
}

func TestRemoveStateIgnoresAMissingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cert-renewal.json")
	if err := certrenew.WriteState(certpair.OSFS{}, path, certrenew.State{Serial: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := certrenew.RemoveState(certpair.OSFS{}, path); err != nil {
		t.Fatal(err)
	}
	if err := certrenew.RemoveState(certpair.OSFS{}, path); err != nil {
		t.Fatalf("second removal: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
