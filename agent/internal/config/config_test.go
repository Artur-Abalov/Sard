// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package config_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
restic:
  password_file: /etc/sard/restic.pass
`

func TestParseReadsAllFields(t *testing.T) {
	c, err := config.Parse([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	want := config.Config{
		Server: config.Server{Address: "sard.example.com:9090"},
		TLS:    config.TLS{CAFile: "/etc/sard/ca.pem", CertFile: "/etc/sard/agent.pem", KeyFile: "/etc/sard/agent.key"},
		Restic: config.Restic{PasswordFile: "/etc/sard/restic.pass"},
	}
	if c != want {
		t.Fatalf("config = %+v", c)
	}
}

func TestParseRequiresServerAddress(t *testing.T) {
	for _, in := range []string{"", "restic:\n  password_file: /x\n"} {
		if _, err := config.Parse([]byte(in)); !errors.Is(err, config.ErrNoServerAddress) {
			t.Errorf("Parse(%q) err = %v", in, err)
		}
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
