// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/refusal"
	"github.com/Artur-Abalov/sard/agent/internal/role"
)

// docs/specs/agent/restic-keys.feature, rules "Верификатор подключает репозиторий паролем своего ключа и учёткой только на чтение"
// and "Верификатор не меняет репозиторий".

const readOnlyKeyID = "KEY-ID-RO"

// verifierWithRepository is a verifier host and an s3 repository that the key
// V opens (its password in the file P); the commands that take the lock fail
// as a key that may only read does.
func verifierWithRepository(t *testing.T, as role.Role) *keysHost {
	t.Helper()
	h := newKeysHost(t)
	h.deps.hostRole = func(string) role.Role { return as }
	r := h.s3Repo()
	r.initialized, r.password = true, "V-PASSWORD"
	h.write(h.path("P"), "V-PASSWORD\n", 0o600)
	return h
}

// addReadOnly is "repo add extra A_S3 --access-key-id KEY-ID-RO --password-from-file P".
func (h *keysHost) addReadOnly() (int, string, string) {
	h.stdinIs(s3Marker)
	return h.sudo("repo", "add", "extra", s3Address, "--access-key-id", readOnlyKeyID, "--secret-key-stdin", "--password-from-file", h.path("P"), "--config", "C")
}

func readOnlyStorage(r *fakeRepo) {
	r.script = func(sub string, _ []string) (string, int, bool) { return lockDenied, 1, sub == "snapshots" }
}

func TestAVerifierConnectsARepositoryWithReadOnlyCredentialsWithoutAWarning(t *testing.T) {
	t.Parallel()
	h := verifierWithRepository(t, role.Verifier)
	readOnlyStorage(h.s3Repo())
	code, _, stderr := h.addReadOnly()
	assertCode(t, code, exitOK)
	if _, err := h.fsys.Stat(h.path("agent.d/repo-extra.yaml")); err != nil {
		t.Errorf("the fragment is not written: %v", err)
	}
	for _, not := range []string{"STORAGE_ACCESS_DENIED", "write access", "can write"} {
		if strings.Contains(stderr, not) {
			t.Errorf("stderr mentions %q:\n%s", not, stderr)
		}
	}
}

func TestAVerifierCredentialsThatCanWriteAreAWarning(t *testing.T) {
	t.Parallel()
	h := verifierWithRepository(t, role.Verifier)
	code, _, stderr := h.addReadOnly()
	assertCode(t, code, exitOK)
	for _, want := range []string{"these storage credentials can write", "verifier does not need to write", "spoil the backups"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

func TestAVerifierDoesNotCreateARepositoryAtAnEmptyAddress(t *testing.T) {
	t.Parallel()
	h := verifierWithRepository(t, role.Verifier)
	h.s3Repo().initialized = false
	before := h.hostTree()
	code, _, stderr := h.addReadOnly()
	assertRefusal(t, code, stderr, exitUsage, "REPOSITORY_NOT_INITIALIZED")
	if n := len(h.restic.callsTo(s3Address, "init")); n != 0 || len(h.s3Repo().keyring.adds) != 0 {
		t.Errorf("restic init %d times, key add %d times", n, len(h.s3Repo().keyring.adds))
	}
	h.assertHostUnchanged(before)
}

func TestAVerifierDoesNotCreateALocalRepositoryEither(t *testing.T) {
	t.Parallel()
	h := newKeysHost(t)
	h.deps.hostRole = verifierRole
	before := h.hostTree()
	code, _, stderr := h.addFresh()
	assertRefusal(t, code, stderr, exitUsage, "REPOSITORY_NOT_INITIALIZED")
	h.assertHostUnchanged(before)
}

func TestAWrongPasswordOfTheVerifierKeyIsRefusedAsBefore(t *testing.T) {
	t.Parallel()
	h := verifierWithRepository(t, role.Verifier)
	h.s3Repo().password = "another"
	before := h.hostTree()
	code, _, stderr := h.addReadOnly()
	assertRefusal(t, code, stderr, exitUsage, "WRONG_PASSWORD")
	h.assertHostUnchanged(before)
}

func TestASourceStillRefusesReadOnlyCredentials(t *testing.T) {
	t.Parallel()
	h := verifierWithRepository(t, role.Source)
	readOnlyStorage(h.s3Repo())
	code, _, stderr := h.addReadOnly()
	assertRefusal(t, code, stderr, exitUsage, "STORAGE_ACCESS_DENIED")
}

// В5.
func TestAnUnregisteredHostWithReadOnlyCredentialsIsToldAboutTheRole(t *testing.T) {
	t.Parallel()
	h := verifierWithRepository(t, role.Source)
	readOnlyStorage(h.s3Repo())
	h.assertAbsent(h.cfg.TLS.CertFile)
	code, _, stderr := h.addReadOnly()
	assertRefusal(t, code, stderr, exitUsage, "STORAGE_ACCESS_DENIED")
	assertReason(t, stderr, "register this host first with a verifier token")
}

// Every command that changes a repository is in this table, so that a new one
// cannot forget the refusal of a verifier: it is refused before restic is
// run, and nothing on the host changes.
func TestARepositoryChangingCommandOnAVerifierIsRefused(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		run    func(h *keysHost) (int, string, string)
		reason string
	}{
		{"repo key add", func(h *keysHost) (int, string, string) {
			return h.sudo("repo", "key", "add", "base", "--for", "recovery")
		}, "VERIFIER_READ_ONLY"},
		{"repo key remove", func(h *keysHost) (int, string, string) {
			return h.sudo("repo", "key", "remove", "base", "deadbeef", "--yes")
		}, "VERIFIER_READ_ONLY"},
		{"repo init", func(h *keysHost) (int, string, string) {
			return h.sudo("repo", "init", "base", "--generate-password")
		}, "VERIFIER_READ_ONLY"},
		{"repo add of a repository that does not exist", func(h *keysHost) (int, string, string) {
			return h.addFresh()
		}, "REPOSITORY_NOT_INITIALIZED"},
	} {
		h := newKeysHost(t)
		h.deps.hostRole = verifierRole
		before := h.hostTree()
		code, _, stderr := c.run(h)
		assertRefusal(t, code, stderr, exitUsage, c.reason)
		h.assertHostUnchanged(before)
		if len(h.restic.callsTo(h.newDir(), "init")) != 0 {
			t.Errorf("%s: restic init was called", c.name)
		}
	}
}

func TestRefuseOnVerifierRefusesOnlyTheCertificateOfAVerifier(t *testing.T) {
	t.Parallel()
	cfg := config.Config{}
	for _, c := range []struct {
		as      role.Role
		refused bool
	}{{role.Verifier, true}, {role.Source, false}} {
		deps := hostDeps{hostRole: func(string) role.Role { return c.as }}
		f := refuseOnVerifier(deps, cfg, "issue or remove keys")
		if (f != nil) != c.refused {
			t.Fatalf("role %v: failure %v", c.as, f)
		}
		if f != nil && (f.Reason != refusal.VerifierReadOnly || !strings.Contains(f.Detail, "issue or remove keys")) {
			t.Errorf("failure %+v", f)
		}
	}
}
