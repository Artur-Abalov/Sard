// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

func hostWithSecret(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte("server:\n  address: s:1\nexecutor:\n  state_dir: /srv/state\nrestore:\n  postgresql_lock_share: 0.25\nsecrets:\n  pg: /etc/sard/pg\n"))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func readSecretFile(path string) ([]byte, error) {
	if path != "/etc/sard/pg" {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	return []byte("pw\n"), nil
}

func TestTheRestoreOfADatabaseUsesTheSettingsOfTheHost(t *testing.T) {
	o := postgresOptions(hostWithSecret(t), readSecretFile)
	if o.StateDir != "/srv/state" || o.LockShare != 0.25 || o.Exec == nil || o.Tools == nil || o.Environ == nil {
		t.Fatalf("options %+v", o)
	}
}

func TestTheSecretsOfTheHostAreReadByName(t *testing.T) {
	o := postgresOptions(hostWithSecret(t), readSecretFile)
	if v, err := o.Secret("pg"); err != nil || string(v) != "pw\n" {
		t.Errorf("Secret(pg) = %q, %v", v, err)
	}
	if _, err := o.Secret("other"); err == nil || err.Error() != `secret "other" is not defined on this host` {
		t.Errorf("an undefined secret: %v", err)
	}
}

func TestWithoutSettingsTheRestoreOfADatabaseUsesTheDefaults(t *testing.T) {
	if o := postgresOptions(config.Config{}, readSecretFile); o.StateDir != defaultExecutorStateDir || o.LockShare != 0.5 {
		t.Errorf("defaults: %+v", o)
	}
}

func TestTheSecretErrorNamesNoPath(t *testing.T) {
	cfg := config.Config{Secrets: map[string]string{"pg": "/etc/sard/pg"}}
	o := postgresOptions(cfg, func(p string) ([]byte, error) { return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrPermission} })
	_, err := o.Secret("pg")
	if err == nil || strings.Contains(err.Error(), "/etc/sard") || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("err = %v", err)
	}
	if _, err := o.Tools.Stat("/nonexistent/../nonexistent"); err == nil {
		t.Error("Stat of a file that is not there")
	}
}

func TestAnErrorThatIsNotAPathErrorIsKeptAsItIs(t *testing.T) {
	boom := errors.New("boom")
	if withoutPath(boom) != boom {
		t.Error("changed")
	}
}
