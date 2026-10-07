// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config_test

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

const full = `
server:
  address: sard.example.com:9090
tls:
  ca_file: /etc/sard/ca.pem
  cert_file: /etc/sard/agent.pem
  key_file: /etc/sard/agent.key
repositories:
  - name: main
    url: s3:https://s3.example.com/backups/db1
    password_file: /etc/sard/main.pass
    env_file: /etc/sard/main.env
  - name: nas
    url: /mnt/nas/restic
    password_file: /etc/sard/nas.pass
    crypto_provider: restic-aes
secrets:
  pg-prod: /etc/sard/secrets/pg-prod
  mikrotik-ssh: /etc/sard/secrets/mikrotik.key
scripts:
  app-maintenance: /usr/local/bin/app-maintenance
restic:
  path: /usr/libexec/sard/restic
  cache_dir: /var/cache/sard/restic
executor:
  state_dir: /var/lib/sard-agent/executor
  max_parallel: 2
`

func TestParseReadsAllFields(t *testing.T) {
	c, err := config.Parse([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		Server: config.Server{Address: "sard.example.com:9090"},
		TLS:    config.TLS{CAFile: "/etc/sard/ca.pem", CertFile: "/etc/sard/agent.pem", KeyFile: "/etc/sard/agent.key"},
		Repositories: []config.Repository{
			{Name: "main", URL: "s3:https://s3.example.com/backups/db1", PasswordFile: "/etc/sard/main.pass", EnvFile: "/etc/sard/main.env"},
			{Name: "nas", URL: "/mnt/nas/restic", PasswordFile: "/etc/sard/nas.pass", CryptoProvider: "restic-aes"},
		},
		Secrets:  map[string]string{"pg-prod": "/etc/sard/secrets/pg-prod", "mikrotik-ssh": "/etc/sard/secrets/mikrotik.key"},
		Scripts:  map[string]string{"app-maintenance": "/usr/local/bin/app-maintenance"},
		Restic:   config.Restic{Path: "/usr/libexec/sard/restic", CacheDir: "/var/cache/sard/restic"},
		Executor: config.Executor{StateDir: "/var/lib/sard-agent/executor", MaxParallel: 2},
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("config = %+v", c)
	}
}

// restic.path and restic.cache_dir are optional; when set they must be
// absolute, so they do not depend on the agent's working directory.
func TestParseRejectsRelativeResticPaths(t *testing.T) {
	cases := map[string]string{
		"restic:\n  path: bin/restic\n":    `restic.path: want an absolute path, got "bin/restic"`,
		"restic:\n  cache_dir: cache\n":    `restic.cache_dir: want an absolute path, got "cache"`,
		"restic:\n  path: ./restic\n":      `restic.path: want an absolute path, got "./restic"`,
		"restic:\n  cache_dir: ~/.cache\n": `restic.cache_dir: want an absolute path, got "~/.cache"`,
	}
	for body, want := range cases {
		_, err := config.Parse([]byte("server:\n  address: s:1\n" + body))
		if !errors.Is(err, config.ErrInvalidRestic) || err.Error() != want {
			t.Errorf("%q: err = %v", body, err)
		}
	}
	if c, err := config.Parse([]byte("server:\n  address: s:1\n")); err != nil || c.Restic != (config.Restic{}) {
		t.Errorf("no restic section: %+v, %v", c.Restic, err)
	}
}

func TestParseRejectsAnInvalidExecutorSection(t *testing.T) {
	cases := map[string]string{
		"executor:\n  state_dir: state\n": `invalid executor setting: executor.state_dir: want an absolute path, got "state"`,
		"executor:\n  max_parallel: -1\n": `invalid executor setting: executor.max_parallel: want zero (one step at a time) or more, got -1`,
	}
	for body, want := range cases {
		_, err := config.Parse([]byte("server:\n  address: s:1\n" + body))
		if !errors.Is(err, config.ErrInvalidExecutor) || err.Error() != want {
			t.Errorf("%q: err = %v", body, err)
		}
	}
	if c, err := config.Parse([]byte("server:\n  address: s:1\n")); err != nil || c.Executor != (config.Executor{}) {
		t.Errorf("no executor section: %+v, %v", c.Executor, err)
	}
}

func TestNamesArePublishedSortedWithoutValues(t *testing.T) {
	c, err := config.Parse([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.SecretNames(); !slices.Equal(got, []string{"mikrotik-ssh", "pg-prod"}) {
		t.Errorf("SecretNames() = %v", got)
	}
	if got := c.ScriptNames(); !slices.Equal(got, []string{"app-maintenance"}) {
		t.Errorf("ScriptNames() = %v", got)
	}
	want := map[string]string{"main": "/etc/sard/main.pass", "nas": "/etc/sard/nas.pass"}
	if got := c.PasswordFiles(); !maps.Equal(got, want) {
		t.Errorf("PasswordFiles() = %v", got)
	}
}

func TestBackendComesFromTheResticURL(t *testing.T) {
	cases := map[string]string{
		"s3:https://s3.example.com/b": "s3",
		"sftp:user@host:/srv/restic":  "sftp",
		"rest:https://host:8000/":     "rest",
		"/mnt/nas/restic":             "local",
		"C:/backups":                  "local",
	}
	for url, want := range cases {
		if got := (config.Repository{URL: url}).Backend(); got != want {
			t.Errorf("Backend(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestParseRequiresServerAddress(t *testing.T) {
	for _, in := range []string{"", "secrets:\n  a: /x\n"} {
		if _, err := config.Parse([]byte(in)); !errors.Is(err, config.ErrNoServerAddress) {
			t.Errorf("Parse(%q) err = %v", in, err)
		}
	}
}

func TestParseRejectsInvalidRepositories(t *testing.T) {
	const head = "server:\n  address: s:1\nrepositories:\n"
	ok := "  - {name: main, url: /r, password_file: /p}\n"
	cases := map[string]string{
		"  - {url: /r, password_file: /p}\n":                    "repositories[0]: invalid repository: name is required",
		ok + "  - {name: main, url: /r2, password_file: /p2}\n": `repositories[1]: invalid repository: duplicate name "main"`,
		"  - {name: main, password_file: /p}\n":                 `repositories[0]: invalid repository: "main": url is required`,
		"  - {name: main, url: /r}\n":                           `repositories[0]: invalid repository: "main": password_file is required`,
	}
	for body, want := range cases {
		_, err := config.Parse([]byte(head + body))
		if !errors.Is(err, config.ErrInvalidRepository) || err.Error() != want {
			t.Errorf("Parse(%q) err = %v, want %q", body, err, want)
		}
	}
	if _, err := config.Parse([]byte(head + ok)); err != nil {
		t.Errorf("valid repository rejected: %v", err)
	}
}

func TestParseRejectsInvalidYAMLAndUnknownKeys(t *testing.T) {
	for _, in := range []string{"server: [", "server:\n  adress: x:1\n"} {
		_, err := config.Parse([]byte(in))
		if err == nil || !strings.HasPrefix(err.Error(), "parse config: ") {
			t.Errorf("Parse(%q) err = %v", in, err)
		}
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(path, []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := config.Load(path); err != nil || c.Server.Address != "sard.example.com:9090" {
		t.Fatalf("Load = %+v, %v", c, err)
	}
	if _, err := config.Load(path + ".missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file err = %v", err)
	}
}
