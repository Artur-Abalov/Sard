// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// Rule "repo init проверяет restic перед работой".

// restic не найден по пути из конфига — отказ до обращения к бэкенду
func TestResticNotFoundAtThePathOfTheConfigIsRefusedBeforeTheBackend(t *testing.T) {
	h := newRepoHost(t)
	q := h.path("nowhere/restic")
	h.cfg.Restic.Path = q
	h.saveConfig()
	before := h.snapshot()
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitAgentError)
	assertReason(t, stderr, "RESTIC_NOT_FOUND")
	for _, want := range []string{"restic.path", q, restic.Minimum.String()} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q: %s", want, stderr)
		}
	}
	h.assertNoBackendCalls()
	h.assertUnchanged(before)
}

// restic не найден рядом с агентом — сообщение называет этот путь
func TestResticNotFoundNextToTheAgentNamesThatPath(t *testing.T) {
	h := newRepoHost(t)
	h.cfg.Restic.Path = ""
	h.saveConfig()
	code, _, stderr := h.initCmd()
	assertCode(t, code, exitAgentError)
	assertReason(t, stderr, "RESTIC_NOT_FOUND")
	for _, want := range []string{h.path("bin/restic"), "restic.path is not set", "in the agent config"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q: %s", want, stderr)
		}
	}
	h.assertNoBackendCalls()
}

// Устаревший restic — отказ с найденной и требуемой версиями
func TestAnOutdatedResticIsRefusedWithBothVersions(t *testing.T) {
	for _, version := range []string{"0.18.1", "0.18.1-dev", "0.17.3", "0.9.6"} {
		h := newRepoHost(t)
		h.restic.version = version
		code, _, stderr := h.initCmd()
		assertCode(t, code, exitAgentError)
		assertReason(t, stderr, "RESTIC_TOO_OLD")
		if !strings.Contains(stderr, version) || !strings.Contains(stderr, restic.Minimum.String()) {
			t.Errorf("%s: stderr = %q", version, stderr)
		}
		h.assertNoBackendCalls()
	}
}

// Непригодный restic — отказ с путём и причиной
func TestAnUnusableResticIsRefusedWithThePathAndTheCause(t *testing.T) {
	h := newRepoHost(t)
	dir := h.path("a-directory")
	plain := h.path("plain-file")
	h.write(plain, "#!/bin/sh\n", 0o644)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(){
		"a directory":    func() { h.cfg.Restic.Path = dir; h.deps.exec = restic.ProcessExecutor{} },
		"not executable": func() { h.cfg.Restic.Path = plain; h.deps.exec = restic.ProcessExecutor{} },
		"prints hello":   func() { h.cfg.Restic.Path = h.path("restic"); h.restic.hello = true },
		"exits with 1":   func() { h.cfg.Restic.Path = h.path("restic"); h.restic.versionFails = true },
	}
	for name, arrange := range cases {
		h.deps.exec = h.restic
		arrange()
		h.saveConfig()
		code, _, stderr := h.initCmd()
		assertCode(t, code, exitAgentError)
		assertReason(t, stderr, "RESTIC_UNUSABLE")
		if !strings.Contains(stderr, filepath.Base(h.cfg.Restic.Path)) {
			t.Errorf("%s: stderr does not name the path: %q", name, stderr)
		}
		h.assertNoBackendCalls()
		h.restic.hello, h.restic.versionFails = false, false
	}
}
