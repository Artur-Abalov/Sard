// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostsetup"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/secretbind"
)

// boundSecrets are the secrets of files, each bound in a directory of the test
// to the plugin postgresql at targets, without TLS allowed: the bindings are
// read from their files as the agent reads them.
func boundSecrets(t *testing.T, files map[string]string, targets ...string) *pluginhost.Secrets {
	t.Helper()
	dir := t.TempDir()
	b, err := secretbind.New("postgresql", targets)
	if err != nil {
		t.Fatal(err)
	}
	b.AllowTLS = "disable"
	for name := range files {
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), b.Marshal(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	in := func(name string) string { return filepath.Join(dir, name+".yaml") }
	return pluginhost.NewSecrets(files, os.ReadFile, secretbind.NewStore(in, uint32(os.Getuid()), secretbind.ReadFile))
}

// configIn is the config yaml written to dir/agent.yaml and loaded from there,
// so that its bindings are those of dir.
func configIn(t *testing.T, dir, yaml string) config.Config {
	t.Helper()
	path := filepath.Join(dir, "agent.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// bindIn binds the secret name, in the bindings next to the config in dir, to
// the plugin postgresql at targets, without TLS allowed.
func bindIn(t *testing.T, dir, name string, targets ...string) {
	t.Helper()
	b, err := secretbind.New("postgresql", targets)
	if err != nil {
		t.Fatal(err)
	}
	b.AllowTLS = "disable"
	l := hostsetup.Layout{Config: filepath.Join(dir, "agent.yaml")}
	if err := os.MkdirAll(l.BindingDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.SecretBinding(name), b.Marshal(), 0o644); err != nil {
		t.Fatal(err)
	}
}
