// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package secrets_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

// loadWithFragments is a main config with one repository and, in agent.d,
// a repository extra and a secret db.
func loadWithFragments(t *testing.T) (cfg config.Config, repoFile, secretFile string) {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) string {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	main := write("agent.yaml", "server: {address: 'a:1'}\nrepositories:\n  - {name: base, url: /b, password_file: /p/base.pass}\n")
	repoFile = write("agent.d/repo-extra.yaml", "repositories:\n  - {name: extra, url: /e, password_file: /p/extra.pass}\n")
	secretFile = write("agent.d/secret-db.yaml", "secrets:\n  db: /p/db\n")
	cfg, err := config.Load(main)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, repoFile, secretFile
}

// Scenario "Нарушение прав файла пароля из фрагмента называет файл фрагмента".
func TestPasswordFileOfAFragmentRepositoryNamesTheFragment(t *testing.T) {
	cfg, repoFile, _ := loadWithFragments(t)
	files := map[string]secrets.Info{
		"/p/base.pass":  {Mode: 0o600, UID: agentUID},
		"/p/extra.pass": {Mode: 0o640, UID: agentUID},
		"/p/db":         {Mode: 0o600, UID: agentUID},
	}
	err := secrets.CheckAll(cfg, agentUID, statOf(files))
	if err == nil {
		t.Fatal("a 0640 password file passed")
	}
	for _, want := range []string{repoFile, "password_file", "/p/extra.pass"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("message %q lacks %q", err, want)
		}
	}
}

func TestSecretOfAFragmentNamesTheFragment(t *testing.T) {
	cfg, _, secretFile := loadWithFragments(t)
	files := map[string]secrets.Info{
		"/p/base.pass":  {Mode: 0o600, UID: agentUID},
		"/p/extra.pass": {Mode: 0o600, UID: agentUID},
		"/p/db":         {Mode: 0o600, UID: 0},
	}
	err := secrets.CheckAll(cfg, agentUID, statOf(files))
	if err == nil || !strings.Contains(err.Error(), secretFile) || !strings.Contains(err.Error(), "secrets.db") {
		t.Fatalf("err = %v, want one naming %s and secrets.db", err, secretFile)
	}
}

func TestMainConfigMessagesStayAsTheyWere(t *testing.T) {
	cfg, _, _ := loadWithFragments(t)
	files := map[string]secrets.Info{
		"/p/base.pass":  {Mode: 0o640, UID: agentUID},
		"/p/extra.pass": {Mode: 0o600, UID: agentUID},
		"/p/db":         {Mode: 0o600, UID: agentUID},
	}
	err := secrets.CheckAll(cfg, agentUID, statOf(files))
	if err == nil || !strings.Contains(err.Error(), "repositories[0].password_file (/p/base.pass)") {
		t.Fatalf("err = %v", err)
	}
}
