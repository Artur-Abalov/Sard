// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package secrets_test

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

const agentUID = 1000

func fullConfig() config.Config {
	return config.Config{
		Server: config.Server{Address: "sard.example.com:9090"},
		TLS:    config.TLS{CAFile: "/etc/sard/ca.pem", CertFile: "/etc/sard/agent.pem", KeyFile: "/etc/sard/agent.key"},
		Repositories: []config.Repository{
			{Name: "main", URL: "s3:https://s3.example.com/backups", PasswordFile: "/etc/sard/main.pass", EnvFile: "/etc/sard/main.env"},
		},
		Secrets: map[string]string{"pg-prod": "/etc/sard/secrets/pg-prod"},
		Scripts: map[string]string{"app-maintenance": "/usr/local/bin/app-maintenance"},
	}
}

// allOwnerOnly answers Stat for fullConfig's secret files, all mode 0600
// (or 0700 for the script) and owned by agentUID; the base for the tests
// below, which override one entry.
func allOwnerOnly() map[string]secrets.Info {
	return map[string]secrets.Info{
		"/etc/sard/agent.key":            {Mode: 0o600, UID: agentUID},
		"/etc/sard/main.pass":            {Mode: 0o600, UID: agentUID},
		"/etc/sard/main.env":             {Mode: 0o600, UID: agentUID},
		"/etc/sard/secrets/pg-prod":      {Mode: 0o600, UID: agentUID},
		"/usr/local/bin/app-maintenance": {Mode: 0o700, UID: agentUID},
		"/etc/sard/ca.pem":               {Mode: 0o644, UID: agentUID},
		"/etc/sard/agent.pem":            {Mode: 0o644, UID: agentUID},
	}
}

func statOf(files map[string]secrets.Info) secrets.StatFunc {
	return func(path string) (secrets.Info, error) {
		info, ok := files[path]
		if !ok {
			return secrets.Info{}, fs.ErrNotExist
		}
		return info, nil
	}
}

// TestSecretFileOpenToGroupOrOthersRefusesStart is the @a1 scenario
// "Секретный файл, доступный группе или остальным, не даёт агенту стартовать".
func TestSecretFileOpenToGroupOrOthersRefusesStart(t *testing.T) {
	cases := []struct {
		key  string
		path string
		mode fs.FileMode
	}{
		{"tls.key_file", "/etc/sard/agent.key", 0o640},
		{"tls.key_file", "/etc/sard/agent.key", 0o604},
		{"tls.key_file", "/etc/sard/agent.key", 0o644},
		{"tls.key_file", "/etc/sard/agent.key", 0o666},
		{"repositories[0].password_file", "/etc/sard/main.pass", 0o640},
		{"repositories[0].password_file", "/etc/sard/main.pass", 0o604},
		{"repositories[0].env_file", "/etc/sard/main.env", 0o640},
		{"repositories[0].env_file", "/etc/sard/main.env", 0o604},
		{"secrets.pg-prod", "/etc/sard/secrets/pg-prod", 0o640},
		{"secrets.pg-prod", "/etc/sard/secrets/pg-prod", 0o604},
		{"scripts.app-maintenance", "/usr/local/bin/app-maintenance", 0o750},
		{"scripts.app-maintenance", "/usr/local/bin/app-maintenance", 0o705},
		{"scripts.app-maintenance", "/usr/local/bin/app-maintenance", 0o755},
	}
	for _, c := range cases {
		t.Run(c.key+"_"+c.mode.String(), func(t *testing.T) {
			files := allOwnerOnly()
			files[c.path] = secrets.Info{Mode: c.mode, UID: agentUID}
			err := secrets.CheckAll(fullConfig(), agentUID, statOf(files))
			if err == nil {
				t.Fatal("want an error: the file is open beyond its owner")
			}
			var serr *secrets.Error
			if !errors.As(err, &serr) {
				t.Fatalf("error type = %T, want *secrets.Error", err)
			}
			if serr.Key != c.key {
				t.Fatalf("key = %q, want %q", serr.Key, c.key)
			}
			if serr.Path != c.path {
				t.Fatalf("path = %q, want %q", serr.Path, c.path)
			}
			if !strings.Contains(err.Error(), c.mode.String()) {
				t.Fatalf("error %q does not name the current mode %v", err.Error(), c.mode)
			}
		})
	}
}

// TestSecretFileOwnedByAnotherUserRefusesStart is the @a1 scenario
// "Секретный файл другого владельца не даёт агенту стартовать".
func TestSecretFileOwnedByAnotherUserRefusesStart(t *testing.T) {
	cases := []struct {
		key  string
		path string
	}{
		{"tls.key_file", "/etc/sard/agent.key"},
		{"repositories[0].password_file", "/etc/sard/main.pass"},
		{"repositories[0].env_file", "/etc/sard/main.env"},
		{"secrets.pg-prod", "/etc/sard/secrets/pg-prod"},
		{"scripts.app-maintenance", "/usr/local/bin/app-maintenance"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			files := allOwnerOnly()
			owner := files[c.path]
			owner.UID = agentUID + 1
			files[c.path] = owner
			err := secrets.CheckAll(fullConfig(), agentUID, statOf(files))
			var serr *secrets.Error
			if !errors.As(err, &serr) {
				t.Fatalf("error type = %T, want *secrets.Error", err)
			}
			if serr.Key != c.key {
				t.Fatalf("key = %q, want %q", serr.Key, c.key)
			}
			if serr.Owner != agentUID+1 || serr.WantOwner != agentUID {
				t.Fatalf("owner = %d, wantOwner = %d", serr.Owner, serr.WantOwner)
			}
		})
	}
}

// TestSecretFileClosedToAllButOwnerIsAccepted is the @a1 scenario
// "Секретный файл, закрытый от всех, кроме владельца, принимается".
func TestSecretFileClosedToAllButOwnerIsAccepted(t *testing.T) {
	cases := []struct {
		path string
		mode fs.FileMode
	}{
		{"/etc/sard/agent.key", 0o600},
		{"/etc/sard/agent.key", 0o400},
		{"/etc/sard/main.pass", 0o600},
		{"/etc/sard/main.pass", 0o400},
		{"/etc/sard/main.env", 0o600},
		{"/etc/sard/secrets/pg-prod", 0o600},
		{"/usr/local/bin/app-maintenance", 0o700},
		{"/usr/local/bin/app-maintenance", 0o500},
	}
	for _, c := range cases {
		t.Run(c.path+"_"+c.mode.String(), func(t *testing.T) {
			files := allOwnerOnly()
			files[c.path] = secrets.Info{Mode: c.mode, UID: agentUID}
			if err := secrets.CheckAll(fullConfig(), agentUID, statOf(files)); err != nil {
				t.Fatalf("CheckAll: %v", err)
			}
		})
	}
}

// TestOpenCertAndCABundleDoNotBlockStart is the @a1 scenario "Открытые на
// чтение сертификат и бандл CA не мешают старту".
func TestOpenCertAndCABundleDoNotBlockStart(t *testing.T) {
	files := allOwnerOnly()
	files["/etc/sard/ca.pem"] = secrets.Info{Mode: 0o644, UID: agentUID + 1}
	files["/etc/sard/agent.pem"] = secrets.Info{Mode: 0o644, UID: agentUID + 1}
	if err := secrets.CheckAll(fullConfig(), agentUID, statOf(files)); err != nil {
		t.Fatalf("CheckAll: %v: tls.cert_file and tls.ca_file are not secret", err)
	}
}

// A secret file that does not exist yet (before enroll writes tls.key_file,
// say) is not a permission problem: whatever tries to use it reports that
// missing file itself.
func TestAMissingSecretFileIsNotACheckAllViolation(t *testing.T) {
	files := allOwnerOnly()
	delete(files, "/etc/sard/agent.key")
	if err := secrets.CheckAll(fullConfig(), agentUID, statOf(files)); err != nil {
		t.Fatalf("CheckAll: %v", err)
	}
}

// A stat failure that is not "file does not exist" (e.g. a permission
// error reading the containing directory) is a genuine problem, unlike a
// missing file, and CheckAll reports it rather than skipping it.
func TestAGenuineStatFailureIsReported(t *testing.T) {
	boom := errors.New("permission denied reading the directory")
	stat := func(path string) (secrets.Info, error) {
		if path == "/etc/sard/agent.key" {
			return secrets.Info{}, boom
		}
		return statOf(allOwnerOnly())(path)
	}
	err := secrets.CheckAll(fullConfig(), agentUID, stat)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestRealStatReportsModeAndOwner(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/secret"
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := secrets.RealStat(path)
	if err != nil {
		t.Fatalf("RealStat: %v", err)
	}
	if info.Mode.Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode.Perm())
	}
	if info.UID != uint32(os.Getuid()) {
		t.Fatalf("uid = %d, want %d", info.UID, os.Getuid())
	}
}

// TestRepositoryWithoutEnvFileNeedsNoEnvFile is the @a1 scenario
// "Репозиторий без env_file не требует файла окружения".
func TestRepositoryWithoutEnvFileNeedsNoEnvFile(t *testing.T) {
	cfg := fullConfig()
	cfg.Repositories[0].EnvFile = ""
	files := allOwnerOnly()
	delete(files, "/etc/sard/main.env")
	if err := secrets.CheckAll(cfg, agentUID, statOf(files)); err != nil {
		t.Fatalf("CheckAll: %v", err)
	}
}
