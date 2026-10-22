// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
)

// docs/specs/agent/restic-keys.feature, rule "check предупреждает о репозитории без ключа восстановления".

func TestARepositoryWithoutARecoveryKeyIsAWarningInRepoCheck(t *testing.T) {
	h := checkHost(t)
	h.base().keyring.keys = []fakeKey{keyH(), keyV()}
	code, stdout, _ := h.repoCheck("base")
	assertCode(t, code, exitWarnings)
	lines, _ := parseOut(t, stdout)
	l := line(t, lines, "repository.keys", "base")
	if l.status != "WARN" || l.message != "no recovery key" || l.hint != "sudo sard-agent repo key add base --for recovery" {
		t.Errorf("%+v", l)
	}
}

func TestASingleKeyIsNamedInTheWarningOfCheck(t *testing.T) {
	h := checkHost(t)
	h.base().keyring.keys = []fakeKey{keyH()}
	_, stdout, _ := h.repoCheck("base")
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, "repository.keys", "base"); l.status != "WARN" || l.message != "no recovery key; only one key" {
		t.Errorf("%+v", l)
	}
}

func TestARepositoryWithARecoveryKeyPassesTheKeyCheck(t *testing.T) {
	h := checkHost(t)
	code, stdout, _ := h.repoCheck("base")
	assertCode(t, code, exitOK)
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, "repository.keys", "base"); l.status != "OK" || l.message != "2 keys, recovery key present" || l.hint != "" {
		t.Errorf("%+v", l)
	}
}

func TestTheKeyStepComesAfterTheIDAndBeforeTheLocks(t *testing.T) {
	h := checkHost(t)
	_, stdout, _ := h.repoCheck("base")
	lines, _ := parseOut(t, stdout)
	ids := checkIDsOf(lines)
	if !slices.Equal(ids[4:7], []string{"repository.id", "repository.keys", "repository.locks"}) {
		t.Errorf("steps %v", ids)
	}
}

func TestCheckChecksTheKeysOfEveryRepository(t *testing.T) {
	h := fullHost(t)
	h.useAddress(h.path("base"), "")
	extra := h.path("extra")
	h.cfg.Repositories = append(h.cfg.Repositories, config.Repository{Name: "extra", URL: extra, PasswordFile: h.path("secrets/base.pass")})
	h.saveHostConfig()
	r := h.restic.repo(extra)
	r.initialized, r.id = true, "REPO-B"
	r.keyring.keys = []fakeKey{keyH()}
	h.announce(announced("base", "REPO-A"), announced("extra", "REPO-B"))
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "repository.keys", "base"); l.status != "OK" {
		t.Errorf("base: %+v", l)
	}
	if l := line(t, lines, "repository.keys", "extra"); l.status != "WARN" || l.message != "no recovery key; only one key" {
		t.Errorf("extra: %+v", l)
	}
}

func TestWithoutTheNetworkTheKeyStepIsSkipped(t *testing.T) {
	h := fullHost(t)
	lines, _, _ := h.checkLines("--offline")
	if l := line(t, lines, "repository.keys", "base"); l.status != "SKIP" {
		t.Errorf("%+v", l)
	}
}

func TestTheKeyStepIsSkippedAfterAWrongPassword(t *testing.T) {
	h := checkHost(t)
	h.base().wrongPassword = true
	_, stdout, _ := h.repoCheck("base")
	lines, _ := parseOut(t, stdout)
	if l := line(t, lines, "repository.keys", "base"); l.status != "SKIP" || l.message != "not checked: repository.password failed" {
		t.Errorf("%+v", l)
	}
}

func TestTheKeyStepIsInTheJSONOfCheck(t *testing.T) {
	h := checkHost(t)
	h.base().keyring.keys = []fakeKey{keyH(), keyV()}
	_, stdout, _ := h.repoCheck("base", "--json")
	for _, row := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(row), &obj); err != nil {
			t.Fatal(err)
		}
		if obj["check"] == "repository.keys" {
			if obj["status"] != "warning" {
				t.Errorf("row %v", obj)
			}
			return
		}
	}
	t.Fatalf("no repository.keys in:\n%s", stdout)
}

func announced(name, id string) hostcheck.AnnouncedRepository {
	return hostcheck.AnnouncedRepository{Name: name, RepositoryID: id}
}
