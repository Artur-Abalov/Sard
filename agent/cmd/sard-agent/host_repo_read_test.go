// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

var cardLine = regexp.MustCompile(`(?m)^(\w+):\s+(.*)$`)

// card parses the "key: value" lines of repo show.
func card(stdout string) map[string]string {
	got := map[string]string{}
	for _, m := range cardLine.FindAllStringSubmatch(stdout, -1) {
		got[m[1]] = m[2]
	}
	return got
}

// Rule "repo show описывает один репозиторий без учётных данных".

func TestCardOfAnInitializedRepository(t *testing.T) {
	h := newSetupHost(t)
	h.base().initialized = true
	code, stdout, _ := h.sudo("repo", "show", "base", "--config", "C")
	assertCode(t, code, exitOK)
	got := card(stdout)
	want := map[string]string{
		"name": "base", "backend": "local", "address": h.path("base"), "status": "initialized",
		"repository_id": h.base().id, "password_file": h.path("secrets/base.pass"), "env_file": "-", "defined_in": h.cfgPath,
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestCardOfARepositoryThatIsNotInitialized(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.sudo("repo", "show", "base", "--config", "C")
	assertCode(t, code, exitOK)
	if got := card(stdout); got["status"] != "not-initialized" || got["repository_id"] != "-" {
		t.Fatalf("card %v", got)
	}
}

func TestThePasswordInTheAddressIsHiddenInTheCard(t *testing.T) {
	h := newSetupHost(t)
	h.cfg.Repositories = append(h.cfg.Repositories, h.cfg.Repositories[0])
	h.cfg.Repositories[1].Name = "offsite"
	h.cfg.Repositories[1].URL = "rest:https://u:" + urlMarker + "@rest.example.com/offsite"
	h.saveConfig()
	code, stdout, stderr := h.sudo("repo", "show", "offsite", "--config", "C")
	assertCode(t, code, exitOK)
	if !strings.Contains(stdout, "rest:https://u:***@rest.example.com/offsite") {
		t.Fatalf("stdout %q", stdout)
	}
	h.assertValuesHidden(stdout, stderr)
}

func TestTheCardOfAFragmentRepositoryNamesTheFragment(t *testing.T) {
	h := newSetupHost(t)
	fragment := h.extraFragment()
	code, stdout, _ := h.sudo("repo", "show", "extra", "--config", "C")
	assertCode(t, code, exitOK)
	if got := card(stdout)["defined_in"]; got != fragment {
		t.Fatalf("defined_in %q", got)
	}
}

func TestCardInJSON(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.sudo("repo", "show", "base", "--json", "--config", "C")
	assertCode(t, code, exitOK)
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	for _, key := range []string{"name", "backend", "address", "status", "repository_id", "password_file", "env_file", "defined_in"} {
		if _, found := got[key]; !found {
			t.Errorf("no %s in %q", key, stdout)
		}
	}
	if got["env_file"] != nil || got["repository_id"] != nil || got["name"] != "base" {
		t.Fatalf("card %v", got)
	}
}

func TestAnUnreachableBackendIsACardWithTheReasonAndATemporaryCode(t *testing.T) {
	h := newSetupHost(t)
	h.base().fatal = "connection refused"
	code, stdout, stderr := h.sudo("repo", "show", "base", "--config", "C")
	assertCode(t, code, exitTemporary)
	got := card(stdout)
	if got["status"] != "BACKEND_UNAVAILABLE" || got["repository_id"] != "-" {
		t.Fatalf("card %v", got)
	}
	if !strings.Contains(stderr, "base: ") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestAnUnknownNameIsRefusedWithTheKnownOnes(t *testing.T) {
	h := newSetupHost(t)
	code, _, stderr := h.sudo("repo", "show", "nope", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "REPOSITORY_UNKNOWN")
	if !strings.Contains(stderr, "base") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestShowCreatesNothingAndCallsResticOnce(t *testing.T) {
	h := newSetupHost(t)
	before := h.hostTree()
	code, _, _ := h.sudo("repo", "show", "base", "--config", "C")
	assertCode(t, code, exitOK)
	h.assertHostUnchanged(before)
	if n := h.restic.backendCalls(); n != 1 {
		t.Fatalf("%d backend calls: %v", n, h.restic.subs())
	}
}

func TestTheServiceUserMayShowARepository(t *testing.T) {
	h := newSetupHost(t)
	code, _, stderr := h.asService("repo", "show", "base", "--config", "C")
	assertCode(t, code, exitOK)
	if strings.Contains(stderr, "PRIVILEGES_REQUIRED") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestShowRefusesAProblemOfTheLocalChecksLikeTheList(t *testing.T) {
	h := newSetupHost(t)
	ok(t, os.Remove(h.path("secrets/base.pass")))
	code, stdout, stderr := h.sudo("repo", "show", "base", "--config", "C")
	assertCode(t, code, exitUsage)
	if card(stdout)["status"] != "PASSWORD_FILE_MISSING" || !strings.Contains(stderr, "PASSWORD_FILE_MISSING") {
		t.Fatalf("stdout %q stderr %q", stdout, stderr)
	}
}

// Rule "Пароль репозитория показывается только по явному флагу".

func TestRevealPrintsThePasswordByteForByteWithAWarning(t *testing.T) {
	h := newSetupHost(t)
	h.write(h.path("secrets/base.pass"), passMarker+"\n", 0o600)
	code, stdout, stderr := h.sudo("repo", "password", "base", "--reveal", "--config", "C")
	assertCode(t, code, exitOK)
	if stdout != passMarker+"\n" {
		t.Fatalf("stdout %q", stdout)
	}
	if !strings.Contains(stderr, "password") || !strings.Contains(stderr, "screen") {
		t.Fatalf("stderr %q", stderr)
	}
	if len(h.log.lines) != 1 || !strings.Contains(h.log.lines[0], "repository base password revealed") || !strings.Contains(h.log.lines[0], "alice") {
		t.Fatalf("audit %q", h.log.lines)
	}
	if strings.Contains(h.log.lines[0], passMarker) {
		t.Fatal("the password is in the audit line")
	}
	h.assertNoBackendCalls()
}

func TestWithoutRevealThePasswordIsNotPrinted(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, stderr := h.sudo("repo", "password", "base", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "REVEAL_REQUIRED")
	if !strings.Contains(stderr, "--reveal") || stdout != "" || len(h.log.lines) != 0 {
		t.Fatalf("stdout %q stderr %q audit %q", stdout, stderr, h.log.lines)
	}
}

func TestThePasswordOfAnUnknownRepositoryIsRefused(t *testing.T) {
	h := newSetupHost(t)
	code, _, stderr := h.sudo("repo", "password", "nope", "--reveal", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "REPOSITORY_UNKNOWN")
}

func TestAMissingPasswordFileIsRefusedWithItsPath(t *testing.T) {
	h := newSetupHost(t)
	ok(t, os.Remove(h.path("secrets/base.pass")))
	code, _, stderr := h.sudo("repo", "password", "base", "--reveal", "--config", "C")
	assertRefusal(t, code, stderr, exitUsage, "PASSWORD_FILE_MISSING")
	if !strings.Contains(stderr, h.path("secrets/base.pass")) {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestTheServiceUserMayRevealAndItIsAudited(t *testing.T) {
	h := newSetupHost(t)
	code, stdout, _ := h.asService("repo", "password", "base", "--reveal", "--config", "C")
	assertCode(t, code, exitOK)
	if stdout != passMarker+"\n" || len(h.log.lines) != 1 {
		t.Fatalf("stdout %q audit %q", stdout, h.log.lines)
	}
}

func TestThePasswordOfAFragmentRepositoryCanBeRevealed(t *testing.T) {
	h := newSetupHost(t)
	h.extraFragment()
	code, stdout, _ := h.sudo("repo", "password", "extra", "--reveal", "--config", "C")
	assertCode(t, code, exitOK)
	if stdout != "extra-password\n" {
		t.Fatalf("stdout %q", stdout)
	}
}

func TestAPasswordFileThatCannotBeReadIsRefusedWithItsPath(t *testing.T) {
	h := newSetupHost(t)
	h.deps.readFile = func(string) ([]byte, error) { return nil, os.ErrPermission }
	code, _, stderr := h.sudo("repo", "password", "base", "--reveal", "--config", "C")
	assertCode(t, code, exitUsage)
	if !strings.Contains(stderr, h.path("secrets/base.pass")) {
		t.Fatalf("stderr %q", stderr)
	}
}

// Rule "repo remove убирает репозиторий только из настройки хоста" is in
// host_repo_add_test.go: it starts from a repository added by repo add.
