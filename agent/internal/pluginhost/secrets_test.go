// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// files is an in-memory ReadFile that counts reads.
type files struct {
	data  map[string]string
	reads int
}

func (f *files) read(name string) ([]byte, error) {
	f.reads++
	data, ok := f.data[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return []byte(data), nil
}

func TestSecretIsReadFromItsFileOnEveryCall(t *testing.T) {
	f := &files{data: map[string]string{"/etc/sard/pg": "s3cret\n"}}
	s := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, f.read)
	for range 2 {
		if v, err := s.Secret("pg"); err != nil || string(v) != "s3cret\n" {
			t.Fatalf("Secret(pg) = %q, %v", v, err)
		}
	}
	if f.reads != 2 {
		t.Errorf("reads = %d, want 2: a rotated secret must be picked up", f.reads)
	}
	if !s.Has("pg") || s.Has("mysql") {
		t.Errorf("Has: pg %v, mysql %v", s.Has("pg"), s.Has("mysql"))
	}
}

func TestUnknownSecretIsATypedError(t *testing.T) {
	f := &files{}
	s := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, f.read)
	_, err := s.Secret("mysql")
	var secretErr *sdk.SecretError
	if !errors.As(err, &secretErr) || secretErr.Name != "mysql" || !errors.Is(err, sdk.ErrUnknownSecret) {
		t.Fatalf("err = %v", err)
	}
	if f.reads != 0 {
		t.Error("an unknown secret must not touch the file system")
	}
}

func TestUnreadableSecretNamesTheSecretAndKeepsTheCause(t *testing.T) {
	s := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, (&files{}).read)
	_, err := s.Secret("pg")
	if !errors.Is(err, fs.ErrNotExist) || errors.Is(err, sdk.ErrUnknownSecret) {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(err.Error(), `secret "pg": `) {
		t.Errorf("Error() = %q", err.Error())
	}
}

// OQ-126: only names leave the host (ADR 0008), so the file's path is not
// in the error, while the secret's name and the cause are.
func TestUnreadableSecretErrorHasNoHostPath(t *testing.T) {
	read := func(string) ([]byte, error) {
		return nil, &fs.PathError{Op: "open", Path: "/etc/x", Err: fs.ErrPermission}
	}
	s := pluginhost.NewSecrets(map[string]string{"pg": "/etc/x"}, read)
	_, err := s.Secret("pg")
	if err == nil || strings.Contains(err.Error(), "/etc/x") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), `"pg"`) || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("err = %v: the name and the cause must stay", err)
	}
}
