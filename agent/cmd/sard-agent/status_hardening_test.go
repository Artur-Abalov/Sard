// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// docs/specs/agent/agent-role.feature, tag @local: sard-agent status.

// Scenario: Статус с испорченным конфигом
func TestStatusNamesTheConfigItCannotLoad(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	code, stdout, stderr := runAgent("status", "--config", missing)
	requireRefusal(t, code, stdout, stderr)
	if !strings.Contains(stderr, "missing.yaml") || strings.Contains(stderr, "not enrolled") {
		t.Fatalf("stderr = %q", stderr)
	}
}

// Scenario: Ошибка флагов статуса — код использования
func TestStatusWithoutArgumentsIsAStatusCommandThatShowsItsUsage(t *testing.T) {
	if !isStatusCommand([]string{"status"}) || isStatusCommand(nil) || isStatusCommand([]string{"enroll", "status"}) {
		t.Fatal("isStatusCommand")
	}
	code, stdout, stderr := runAgent("status")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "--config <path> is required") || !strings.Contains(stderr, "-config string") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestAnAgentIsEnrolledWhenItsCertificateOrItsKeyIsThere(t *testing.T) {
	exists := func(string) (os.FileInfo, error) { return nil, nil }
	absent := func(string) (os.FileInfo, error) { return nil, fs.ErrNotExist }
	only := func(name string) func(string) (os.FileInfo, error) {
		return func(p string) (os.FileInfo, error) {
			if p == name {
				return nil, nil
			}
			return nil, fs.ErrNotExist
		}
	}
	rows := []struct {
		name  string
		stat  func(string) (os.FileInfo, error)
		files config.TLS
		want  bool
	}{
		{"both there", exists, config.TLS{CertFile: "c", KeyFile: "k"}, true},
		{"neither there", absent, config.TLS{CertFile: "c", KeyFile: "k"}, false},
		{"only the key is there", only("k"), config.TLS{CertFile: "c", KeyFile: "k"}, true},
		{"only the certificate is there", only("c"), config.TLS{CertFile: "c", KeyFile: "k"}, true},
		{"no certificate path, the key is there", exists, config.TLS{KeyFile: "k"}, true},
		{"no paths at all, whatever the file system says", exists, config.TLS{}, false},
		{"no key path, no certificate there", exists, config.TLS{CertFile: "c"}, true},
		{"no key path, the certificate is missing", absent, config.TLS{CertFile: "c"}, false},
	}
	for _, row := range rows {
		if got := (statusDeps{stat: row.stat}).enrolled(row.files); got != row.want {
			t.Errorf("%s: enrolled = %v", row.name, got)
		}
	}
}
