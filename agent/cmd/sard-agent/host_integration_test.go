// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Integration tests of repo add with the pinned restic and a local
// repository in D: the scenarios tagged @integration of host-setup.feature.

// keepsTheCaller runs the real restic as the user of the test, after
// checking that the command asked for the service user: a test is not root
// and cannot become another user.
type keepsTheCaller struct {
	restic.ProcessExecutor
	t    *testing.T
	want restic.RunAs
}

func (e keepsTheCaller) Run(ctx context.Context, cmd restic.Command) (int, error) {
	if cmd.RunAs == nil || *cmd.RunAs != e.want {
		e.t.Errorf("restic %v ran as %+v, want %+v", cmd.Args, cmd.RunAs, e.want)
	}
	cmd.RunAs = nil
	return e.ProcessExecutor.Run(ctx, cmd)
}

func realSetupHost(t *testing.T) *setupHost {
	t.Helper()
	h := newSetupHost(t)
	h.cfg.Restic.Path = pinnedRestic(t)
	h.saveConfig()
	h.deps.exec = keepsTheCaller{t: t, want: restic.RunAs{UID: serviceUID, GID: serviceUID}}
	h.deps.pathEnv = os.Getenv("PATH")
	return h
}

func (h *setupHost) idOf(url, passwordFile string) (string, error) {
	repo := config.Repository{Name: "extra", URL: url, PasswordFile: passwordFile}
	return repositoryID(h.t, h.repoHost, repo)
}

// Пустое хранилище создаётся со сгенерированным паролем
func TestIntegrationRepoAddCreatesARepositoryThePasswordFileOpens(t *testing.T) {
	h := realSetupHost(t)
	code, stdout, stderr := h.add()
	assertCode(t, code, exitOK)
	id, err := h.idOf(h.extraDir(), h.path("secrets/restic-extra.pass"))
	if err != nil {
		t.Fatalf("the repository does not open: %v (stderr %q)", err, stderr)
	}
	if !strings.Contains(stdout, id) || !hexID.MatchString(stdout) {
		t.Fatalf("stdout %q, id %s", stdout, id)
	}
	if !base64URL43.MatchString(h.fileContent(h.path("secrets/restic-extra.pass"))) {
		t.Fatal("the password file is not one line of 43 base64url characters")
	}
	h.assertValuesHidden(stdout, stderr)
}

// Существующий репозиторий подключается по его паролю из стандартного ввода
func TestIntegrationRepoAddAttachesAnExistingRepositoryByItsPassword(t *testing.T) {
	h := realSetupHost(t)
	other := h.path("outside.pass")
	h.write(other, passMarker+"\n", 0o600)
	h.cfg.Repositories = append(h.cfg.Repositories, config.Repository{Name: "extra", URL: h.extraDir(), PasswordFile: other})
	h.saveConfig()
	assertCode(t, first(h.sudo("repo", "init", "--config", "C", "extra")), exitOK)
	id, err := h.idOf(h.extraDir(), other)
	if err != nil {
		t.Fatal(err)
	}
	h.cfg.Repositories = h.cfg.Repositories[:1]
	h.saveConfig()
	h.stdinIs(passMarker + "\n")
	code, stdout, stderr := h.add("--password-stdin")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "attached an existing repository") || !strings.Contains(stdout, id) {
		t.Fatalf("stdout %q", stdout)
	}
	if h.fileContent(h.path("secrets/restic-extra.pass")) != passMarker+"\n" {
		t.Fatal("the password file does not hold the password given")
	}
	h.assertValuesHidden(stdout, stderr)
}

// Неверный пароль существующего репозитория — отказ без следов
func TestIntegrationRepoAddWithAWrongPasswordTouchesNothing(t *testing.T) {
	h := realSetupHost(t)
	other := h.path("outside.pass")
	h.write(other, "the real password\n", 0o600)
	h.cfg.Repositories = append(h.cfg.Repositories, config.Repository{Name: "extra", URL: h.extraDir(), PasswordFile: other})
	h.saveConfig()
	assertCode(t, first(h.sudo("repo", "init", "--config", "C", "extra")), exitOK)
	h.cfg.Repositories = h.cfg.Repositories[:1]
	h.saveConfig()
	before := hashes(t, h.extraDir())
	h.stdinIs(passMarker + "\n")
	code, stdout, stderr := h.add("--password-stdin")
	assertRefusal(t, code, stderr, exitUsage, "WRONG_PASSWORD")
	h.assertAbsent(h.path("agent.d/repo-extra.yaml"), h.path("secrets/restic-extra.pass"))
	h.assertNoTemporaryFiles(h.secretsDir())
	if hashes(t, h.extraDir()) != before {
		t.Error("the data of the repository changed")
	}
	h.assertValuesHidden(stdout, stderr)
}
