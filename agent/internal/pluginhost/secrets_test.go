// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// db is the target the plugins of the tests send their secrets to.
var db = sdk.Target{Host: "db", Port: 5432, Database: "app"}

// bound are fake Bindings: every secret is bound to plugin at targets (db
// when none), allowing plain TCP; none: no secret is bound; err: no binding
// can be judged. reads counts the lookups.
type bound struct {
	plugin  string
	targets []string
	none    bool
	err     error
	reads   *int
}

func (b bound) Binding(string) (secretbind.Binding, bool, error) {
	if b.reads != nil {
		*b.reads++
	}
	if b.none || b.err != nil {
		return secretbind.Binding{}, false, b.err
	}
	targets := b.targets
	if len(targets) == 0 {
		targets = []string{db.String()}
	}
	binding, err := secretbind.New(b.plugin, targets)
	binding.AllowTLS = "disable"
	return binding, err == nil, err
}

// secretOf is the value of the secret called name for db.
func secretOf(h interface {
	SecretFor(string, sdk.Target) (sdk.Secret, error)
}, name string) ([]byte, error) {
	s, err := h.SecretFor(name, db)
	return s.Value, err
}

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
	s := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, f.read, bound{plugin: "postgresql"})
	for range 2 {
		if v, err := s.SecretFor("postgresql", "pg", db); err != nil || string(v.Value) != "s3cret\n" || v.Grant.TLS != "disable" {
			t.Fatalf("SecretFor(pg) = %+v, %v", v, err)
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
	s := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, f.read, bound{plugin: "postgresql"})
	_, err := s.SecretFor("postgresql", "mysql", db)
	var secretErr *sdk.SecretError
	if !errors.As(err, &secretErr) || secretErr.Name != "mysql" || !errors.Is(err, sdk.ErrUnknownSecret) {
		t.Fatalf("err = %v", err)
	}
	if f.reads != 0 {
		t.Error("an unknown secret must not touch the file system")
	}
}

func TestUnreadableSecretNamesTheSecretAndKeepsTheCause(t *testing.T) {
	s := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, (&files{}).read, bound{plugin: "postgresql"})
	_, err := s.SecretFor("postgresql", "pg", db)
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
	s := pluginhost.NewSecrets(map[string]string{"pg": "/etc/x"}, read, bound{plugin: "postgresql"})
	_, err := s.SecretFor("postgresql", "pg", db)
	if err == nil || strings.Contains(err.Error(), "/etc/x") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), `"pg"`) || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("err = %v: the name and the cause must stay", err)
	}
}

// S11: the value is read only for the plugin and the targets of the binding.
func TestASecretIsReadOnlyForThePluginAndTheTargetsOfItsBinding(t *testing.T) {
	evil := sdk.Target{Host: "evil.example", Port: 5432, Database: "app"}
	broken := errors.New(`binding of secret "pg": not a regular file`)
	cases := map[string]struct {
		bindings pluginhost.Bindings
		target   sdk.Target
		want     error
	}{
		"another target":   {bound{plugin: "postgresql"}, evil, &sdk.BindingError{Name: "pg", Plugin: "postgresql", Target: evil}},
		"another plugin":   {bound{plugin: "files"}, db, &sdk.BindingError{Name: "pg", Plugin: "postgresql", Target: db, BoundTo: "files"}},
		"no binding":       {bound{none: true}, db, &sdk.BindingError{Name: "pg", Plugin: "postgresql", Target: db, Unbound: true}},
		"a broken binding": {bound{err: broken}, db, broken},
	}
	for name, c := range cases {
		f := &files{data: map[string]string{"/etc/sard/pg": "s3cret"}}
		s := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, f.read, c.bindings)
		got, err := s.SecretFor("postgresql", "pg", c.target)
		if !reflect.DeepEqual(err, c.want) || got.Value != nil {
			t.Errorf("%s: SecretFor = %+v, %#v; want %#v", name, got, err, c.want)
		}
		notRead(t, name, f, err)
	}
}

// notRead: the file of the secret was not read and its value is not in err.
func notRead(t *testing.T, name string, f *files, err error) {
	t.Helper()
	if err == nil || strings.Contains(err.Error(), "s3cret") || f.reads != 0 {
		t.Errorf("%s: err %v, %d reads of the file", name, err, f.reads)
	}
}

func TestAnUnknownSecretIsNotLookedUpInTheBindings(t *testing.T) {
	reads := 0
	s := pluginhost.NewSecrets(map[string]string{"pg": "/etc/sard/pg"}, (&files{}).read, bound{plugin: "postgresql", reads: &reads})
	if _, err := s.Allow("postgresql", "mysql", db); !errors.Is(err, sdk.ErrUnknownSecret) || reads != 0 {
		t.Errorf("Allow(mysql) = %v, %d lookups", err, reads)
	}
	if g, err := s.Allow("postgresql", "pg", db); err != nil || g.TLS != "disable" || reads != 1 {
		t.Errorf("Allow(pg) = %+v, %v, %d lookups", g, err, reads)
	}
}
