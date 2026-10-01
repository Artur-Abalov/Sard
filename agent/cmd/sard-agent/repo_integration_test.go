// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

// Integration tests of repo init and repo list with the pinned restic
// (scripts/fetch-restic.sh) and a local repository in temporary directories:
//
//	go test -tags integration ./cmd/sard-agent/...
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

func pinnedRestic(t *testing.T) string {
	t.Helper()
	bin, err := filepath.Abs(filepath.Join("..", "..", "..", ".bin", "restic", restic.Pinned.String(), "linux_"+runtime.GOARCH, "restic"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("pinned restic missing, run scripts/fetch-restic.sh: %v", err)
	}
	return bin
}

// realHost is the conventions section of the spec with the real restic:
// both repositories are local, so no network is involved unless a test wants it.
func realHost(t *testing.T) *repoHost {
	t.Helper()
	h := newRepoHost(t)
	h.cfg.Restic.Path = pinnedRestic(t)
	h.cfg.Repositories[1].URL = h.path("repo-offsite")
	h.saveConfig()
	h.deps = productionRepoDeps()
	h.deps.uid = uint32(os.Getuid())
	return h
}

// repositoryID opens the repository with the password file, as the agent would.
func repositoryID(t *testing.T, h *repoHost, repo config.Repository) (string, error) {
	t.Helper()
	cli := restic.New(restic.Options{
		Binary: pinnedRestic(t), CacheDir: h.path("cache"), Path: os.Getenv("PATH"), Exec: restic.ProcessExecutor{},
		Keys: crypto.NewResticAES(map[string]string{repo.Name: repo.PasswordFile}), ReadFile: os.ReadFile,
	}, repo)
	return cli.ID(context.Background())
}

// hashes is "the data of the repository": every file with its SHA-256.
func hashes(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			data, _ := os.ReadFile(p)
			sum := sha256.Sum256(data)
			lines = append(lines, p+" "+hex.EncodeToString(sum[:]))
		}
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// Команда создаёт репозиторий, который открывается файлом пароля из конфига
func TestIntegrationTheCommandCreatesARepositoryThatOpensWithThePasswordFile(t *testing.T) {
	h := realHost(t)
	code, stdout, stderr := h.initCmd()
	assertCode(t, code, exitOK)
	id, err := repositoryID(t, h, h.cfg.Repositories[0])
	if err != nil {
		t.Fatalf("the repository does not open: %v (stderr %q)", err, stderr)
	}
	if !strings.Contains(stdout, id) || !hexID.MatchString(stdout) || !strings.Contains(stdout, "local") || !strings.Contains(stdout, `"main"`) {
		t.Fatalf("stdout = %q, restic id = %s", stdout, id)
	}
	assertNoSecrets(t, stdout, stderr)
}

// Переменные restic из окружения оператора не влияют на репозиторий
func TestIntegrationResticVariablesOfTheOperatorAreIgnored(t *testing.T) {
	h := realHost(t)
	other := h.path("other-repo")
	t.Setenv("RESTIC_REPOSITORY", other)
	t.Setenv("RESTIC_PASSWORD", "operator-password")
	assertCode(t, first(h.initCmd()), exitOK)
	if _, err := repositoryID(t, h, h.cfg.Repositories[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err == nil {
		t.Fatal("something was created in RESTIC_REPOSITORY")
	}
}

// Файл пароля из одного перевода строки не даёт создать репозиторий
func TestIntegrationAPasswordFileOfOneLineBreakCreatesNoRepository(t *testing.T) {
	h := realHost(t)
	h.write(h.pass(), "\n", 0o600)
	code, _, stderr := h.initCmd()
	if code == exitOK {
		t.Fatal("the command succeeded")
	}
	if _, err := os.Stat(filepath.Join(h.repoURL(), "config")); err == nil {
		t.Fatalf("a repository config exists; stderr %q", stderr)
	}
}

// Уже инициализированный репозиторий — отдельный отказ с его id
func TestIntegrationAnInitialisedRepositoryIsRefusedAndItsDataIsUntouched(t *testing.T) {
	h := realHost(t)
	assertCode(t, first(h.initCmd()), exitOK)
	id, _ := repositoryID(t, h, h.cfg.Repositories[0])
	before, hostBefore := hashes(t, h.repoURL()), h.snapshotWithout(h.repoURL(), h.path("cache"))
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitIdentityExists)
	assertReason(t, stderr, "REPOSITORY_EXISTS")
	if !strings.Contains(stderr, id) {
		t.Errorf("stderr does not name %s: %q", id, stderr)
	}
	if hashes(t, h.repoURL()) != before {
		t.Error("the data of the repository changed")
	}
	h.assertUnchangedWithout(hostBefore, h.repoURL(), h.path("cache"))
}

// Репозиторий по адресу есть, но файл пароля его не открывает
func TestIntegrationARepositoryThePasswordFileDoesNotOpenIsAUsageError(t *testing.T) {
	h := realHost(t)
	assertCode(t, first(h.initCmd()), exitOK)
	h.write(h.pass(), "another password\n", 0o600)
	before := hashes(t, h.repoURL())
	code, stdout, stderr := h.initCmd()
	assertCode(t, code, exitUsage)
	assertReason(t, stderr, "WRONG_PASSWORD")
	if hashes(t, h.repoURL()) != before {
		t.Error("the data of the repository changed")
	}
	assertNoSecrets(t, stdout, stderr)
}

// Недоступный REST-бэкенд с паролем в адресе не раскрывает пароль
func TestIntegrationAnUnreachableRESTBackendDoesNotRevealTheURLPassword(t *testing.T) {
	h := realHost(t)
	h.cfg.Repositories[1].URL = offsiteURL
	h.saveConfig()
	code, stdout, stderr := h.run("init", "--config", "C", "--timeout", "3s", "offsite")
	assertCode(t, code, exitTemporary)
	if !strings.Contains(stderr, "rest") {
		t.Errorf("stderr does not name the backend type: %q", stderr)
	}
	assertNoSecrets(t, stdout, stderr)
}

// Настоящий restic не раскрывает пароль при успехе и неверном пароле
func TestIntegrationResticDoesNotRevealThePassword(t *testing.T) {
	h := realHost(t)
	code1, out1, err1 := h.initCmd()
	assertCode(t, code1, exitOK)
	h.write(h.pass(), passMarker+"-2\n", 0o600)
	code2, out2, err2 := h.initCmd()
	if code2 == exitOK {
		t.Fatal("the second run succeeded")
	}
	assertNoSecrets(t, out1, err1, out2, err2)
}

// Список называет инициализированный и неинициализированный репозитории
func TestIntegrationTheListNamesInitialisedAndNotInitialisedRepositories(t *testing.T) {
	h := realHost(t)
	assertCode(t, first(h.initCmd()), exitOK)
	id, _ := repositoryID(t, h, h.cfg.Repositories[0])
	code, stdout, _ := h.listCmd()
	assertCode(t, code, exitOK)
	rows := listRows(t, stdout)
	if got := strings.Join(rows["main"], " "); got != "main local initialized "+id {
		t.Errorf("main = %q", got)
	}
	if got := strings.Join(rows["offsite"], " "); got != "offsite local not-initialized -" {
		t.Errorf("offsite = %q", got)
	}
}

// Список ничего не создаёт и не меняет
func TestIntegrationTheListCreatesNothing(t *testing.T) {
	h := realHost(t)
	before := h.snapshotWithout(h.path("cache"))
	code, _, _ := h.listCmd()
	assertCode(t, code, exitOK)
	if _, err := os.Stat(h.repoURL()); err == nil {
		t.Fatal("the repository directory was created")
	}
	h.assertUnchangedWithout(before, h.path("cache"))
}

// snapshotWithout is snapshot without the trees the command may write to
// (the repository itself, restic's cache).
func (h *repoHost) snapshotWithout(skip ...string) map[string]string {
	h.t.Helper()
	got := h.snapshot()
	dropTrees(got, skip)
	return got
}
