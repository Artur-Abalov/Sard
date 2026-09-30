// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package repoinit_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/repoinit"
	"github.com/Artur-Abalov/sard/agent/internal/secrets"
)

const uid = 1000

// files is an in-memory host: stat answers and file contents.
type files struct {
	info map[string]secrets.Info
	data map[string]string
}

func (f files) host() repoinit.Host {
	return repoinit.Host{
		UID: uid,
		Stat: func(p string) (secrets.Info, error) {
			info, ok := f.info[p]
			if !ok {
				return secrets.Info{}, fs.ErrNotExist
			}
			return info, nil
		},
		ReadFile: func(p string) ([]byte, error) {
			d, ok := f.data[p]
			if !ok {
				return nil, fs.ErrNotExist
			}
			return []byte(d), nil
		},
	}
}

func goodHost() files {
	return files{
		info: map[string]secrets.Info{
			"/etc/sard/main.pass": {Mode: 0o600, UID: uid, Size: 12},
			"/etc/sard/main.env":  {Mode: 0o600, UID: uid, Size: 30},
		},
		data: map[string]string{"/etc/sard/main.env": "AWS_SECRET_ACCESS_KEY=ENV-MARKER\n"},
	}
}

var repo = config.Repository{Name: "main", URL: "s3:https://x/b", PasswordFile: "/etc/sard/main.pass", EnvFile: "/etc/sard/main.env"}

func TestPreflightAcceptsAGoodRepositoryAndReturnsItsEnvironment(t *testing.T) {
	checked, f := repoinit.Preflight(goodHost().host(), repo, 0, false)
	if f != nil || checked.PasswordMissing || len(checked.EnvAssignments) != 1 {
		t.Fatalf("%+v, %v", checked, f)
	}
	for _, provider := range []string{"", "restic-aes"} {
		r := repo
		r.CryptoProvider = provider
		if _, f := repoinit.Preflight(goodHost().host(), r, 0, false); f != nil {
			t.Errorf("provider %q: %v", provider, f)
		}
	}
}

func TestPreflightRefusesEachProblemWithItsReason(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(h files, r *config.Repository, create *bool)
		reason  repoinit.Reason
		class   repoinit.Class
		text    string
	}{
		{"provider", func(_ files, r *config.Repository, _ *bool) { r.CryptoProvider = "gost" },
			repoinit.CryptoProviderUnsupported, repoinit.ClassUsage, `"gost"`},
		{"no password file", func(h files, _ *config.Repository, _ *bool) { delete(h.info, "/etc/sard/main.pass") },
			repoinit.PasswordFileMissing, repoinit.ClassUsage, "--generate-password"},
		{"empty password file", func(h files, _ *config.Repository, _ *bool) {
			h.info["/etc/sard/main.pass"] = secrets.Info{Mode: 0o600, UID: uid}
		}, repoinit.PasswordFileEmpty, repoinit.ClassUsage, "is empty"},
		{"wide password file", func(h files, _ *config.Repository, _ *bool) {
			h.info["/etc/sard/main.pass"] = secrets.Info{Mode: 0o640, UID: uid, Size: 1}
		}, repoinit.SecretFileRejected, repoinit.ClassUsage, "repositories[3].password_file"},
		{"env file of another owner", func(h files, _ *config.Repository, _ *bool) {
			h.info["/etc/sard/main.env"] = secrets.Info{Mode: 0o600, UID: 0, Size: 1}
		}, repoinit.SecretFileRejected, repoinit.ClassUsage, "repositories[3].env_file"},
		{"no env file", func(h files, _ *config.Repository, _ *bool) { delete(h.data, "/etc/sard/main.env") },
			repoinit.EnvFileMissing, repoinit.ClassUsage, "env_file"},
		{"bad env file", func(h files, _ *config.Repository, _ *bool) {
			h.data["/etc/sard/main.env"] = "A=b\nLD_PRELOAD=ENV-MARKER\n"
		},
			repoinit.EnvFileInvalid, repoinit.ClassUsage, "line 2"},
	}
	for _, c := range cases {
		h, r, create := goodHost(), repo, false
		c.arrange(h, &r, &create)
		_, f := repoinit.Preflight(h.host(), r, 3, create)
		if f == nil || f.Reason != c.reason || f.Class != c.class || !strings.Contains(f.Error(), c.text) || strings.Contains(f.Error(), "ENV-MARKER") {
			t.Errorf("%s: %v", c.name, f)
		}
	}
}

func TestPreflightLetsAMissingPasswordFilePassWhenItMayBeCreated(t *testing.T) {
	h := goodHost()
	delete(h.info, "/etc/sard/main.pass")
	checked, f := repoinit.Preflight(h.host(), repo, 0, true)
	if f != nil || !checked.PasswordMissing {
		t.Fatalf("%+v, %v", checked, f)
	}
}

func TestPreflightChecksTheProviderBeforeTheFiles(t *testing.T) {
	r := repo
	r.CryptoProvider = "gost"
	_, f := repoinit.Preflight(files{}.host(), r, 0, false)
	if f.Reason != repoinit.CryptoProviderUnsupported {
		t.Fatalf("%v", f)
	}
}

func TestASecretFileRejectionIsPrintedAsA1WroteIt(t *testing.T) {
	h := goodHost()
	h.info["/etc/sard/main.pass"] = secrets.Info{Mode: 0o644, UID: uid, Size: 1}
	_, f := repoinit.Preflight(h.host(), repo, 0, false)
	if !strings.HasPrefix(f.Error(), "secret file repositories[0].password_file (/etc/sard/main.pass) has mode -rw-r--r--") || strings.Contains(f.Error(), "SECRET_FILE_REJECTED") {
		t.Fatalf("%q", f.Error())
	}
}

func TestAnUnknownRepositoryListsTheKnownNames(t *testing.T) {
	f := repoinit.UnknownRepository("backup", "/etc/sard/agent.yaml", []string{"main", "offsite"})
	if f.Reason != repoinit.RepositoryUnknown || f.Class != repoinit.ClassUsage ||
		f.Error() != `REPOSITORY_UNKNOWN: no repository named "backup" in /etc/sard/agent.yaml; configured repositories: main, offsite` {
		t.Errorf("%q", f.Error())
	}
	if got := repoinit.UnknownRepository("x", "/c.yaml", nil).Error(); got != "REPOSITORY_UNKNOWN: no repositories configured in /c.yaml" {
		t.Errorf("%q", got)
	}
}

func TestAcquireLockRefusesASecondInit(t *testing.T) {
	cache := t.TempDir()
	r := config.Repository{Name: "main"}
	unlock, f := repoinit.AcquireLock(os.OpenFile, cache, r)
	if f != nil {
		t.Fatal(f)
	}
	defer unlock()
	_, f = repoinit.AcquireLock(os.OpenFile, cache, r)
	if f == nil || f.Reason != repoinit.InitInProgress || f.Class != repoinit.ClassTemporary {
		t.Fatalf("%v", f)
	}
}

func TestAcquireLockNamesTheCacheDirItCannotUse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	_, f := repoinit.AcquireLock(os.OpenFile, dir, config.Repository{Name: "main"})
	if f == nil || f.Reason != repoinit.LockWrite || f.Class != repoinit.ClassWrite ||
		!strings.Contains(f.Detail, "restic.cache_dir") || !strings.Contains(f.Detail, dir) || !strings.Contains(f.Detail, "no such file") {
		t.Fatalf("%v", f)
	}
}

func TestCreatePasswordWritesThePassword(t *testing.T) {
	var path, data string
	write := func(p string, d []byte) error { path, data = p, string(d); return nil }
	password, err := repoinit.CreatePassword(write, strings.NewReader(strings.Repeat("k", 32)), repo)
	if err != nil || path != repo.PasswordFile || data != password+"\n" || len(password) != 43 {
		t.Fatalf("%q %v; wrote %q to %q", password, err, data, path)
	}
}

func TestCreatePasswordExplainsAWriteFailure(t *testing.T) {
	failing := func(string, []byte) error { return fs.ErrPermission }
	_, err := repoinit.CreatePassword(failing, strings.NewReader(strings.Repeat("k", 32)), repo)
	var f *repoinit.Failure
	if !errors.As(err, &f) || f.Reason != repoinit.PasswordFileWrite || !strings.Contains(f.Detail, "/etc/sard") {
		t.Fatalf("%v", err)
	}
}

func TestCreatePasswordReportsAGeneratorFailureAsNoWriteFailure(t *testing.T) {
	write := func(string, []byte) error { t.Fatal("nothing must be written"); return nil }
	_, err := repoinit.CreatePassword(write, strings.NewReader("short"), repo)
	var f *repoinit.Failure
	if err == nil || errors.As(err, &f) {
		t.Fatalf("%v", err)
	}
}

func TestPreflightAcceptsTheNameOfTheBuiltInProvider(t *testing.T) {
	r := repo
	r.CryptoProvider = crypto.NewResticAES(nil).Name()
	if _, f := repoinit.Preflight(goodHost().host(), r, 0, false); f != nil {
		t.Fatalf("%v", f)
	}
	if crypto.ResticAESName != "restic-aes" {
		t.Fatalf("ResticAESName = %q", crypto.ResticAESName)
	}
}
