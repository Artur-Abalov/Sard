// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/restic"
)

// docs/specs/agent/restic-keys.feature, @integration: the pinned restic and a
// local repository in temporary directories.

// realKeysHost is a keysHost whose repository base is a real restic repository
// opened by the password file of the host.
func realKeysHost(t *testing.T) *keysHost {
	t.Helper()
	h := newKeysHost(t)
	h.cfg.Restic.Path = pinnedRestic(t)
	h.saveConfig()
	h.deps.exec = keepsTheCaller{t: t, want: restic.RunAs{UID: serviceUID, GID: serviceUID}}
	ok(t, os.MkdirAll(h.path("out"), 0o755))
	code, _, stderr := h.sudo("repo", "init", "base", "--config", "C", "--no-recovery-key")
	if code != exitOK {
		t.Fatalf("repo init: code %d, stderr %q", code, stderr)
	}
	return h
}

// resticAs runs the pinned restic against base with a password file.
func (h *keysHost) resticAs(passwordFile string, args ...string) (string, int) {
	h.t.Helper()
	cmd := exec.Command(pinnedRestic(h.t), args...)
	cmd.Env = []string{"RESTIC_REPOSITORY=" + h.path("base"), "RESTIC_PASSWORD_FILE=" + passwordFile, "RESTIC_CACHE_DIR=" + h.path("cache"), "PATH=" + os.Getenv("PATH")}
	out, err := cmd.Output()
	if ee, isExit := err.(*exec.ExitError); isExit {
		return string(out), ee.ExitCode()
	}
	if err != nil {
		h.t.Fatal(err)
	}
	return string(out), 0
}

func TestIntegrationAnIssuedKeyOpensTheRepositoryWithTheRealRestic(t *testing.T) {
	t.Parallel()
	h := realKeysHost(t)
	vk := filepath.Join(h.path("out"), "vk")
	code, _, stderr := h.keyCmd("add", "base", "--for", "verifier", "--to-file", vk)
	if code != exitOK {
		t.Fatalf("key add: code %d, stderr %q", code, stderr)
	}
	if _, exit := h.resticAs(vk, "snapshots"); exit != 0 {
		t.Errorf("restic snapshots with the new password ended with %d", exit)
	}
	out, _ := h.resticAs(h.path("secrets/base.pass"), "key", "list", "--json")
	var keys []struct {
		UserName string `json:"userName"`
		HostName string `json:"hostName"`
	}
	if err := json.Unmarshal([]byte(out), &keys); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, k := range keys {
		if k.UserName == "sard:verifier" && k.HostName == "src1" {
			return
		}
	}
	t.Errorf("no key with userName sard:verifier and the host name of this host in %s", out)
}

// verifierKeyID is the full id of the key issued for the verifier, from repo key list.
func (h *keysHost) verifierKeyID() string {
	h.t.Helper()
	_, listing, _ := h.keyCmd("list", "base", "--json")
	var list struct {
		Keys []struct {
			ID      string  `json:"id"`
			Purpose *string `json:"purpose"`
		} `json:"keys"`
	}
	if err := json.Unmarshal([]byte(listing), &list); err != nil {
		h.t.Fatalf("%v: %s", err, listing)
	}
	for _, k := range list.Keys {
		if k.Purpose != nil && *k.Purpose == "verifier" {
			return k.ID
		}
	}
	h.t.Fatalf("no verifier key in %s", listing)
	return ""
}

func TestIntegrationARemovedKeyNoLongerOpensTheRepository(t *testing.T) {
	t.Parallel()
	h := realKeysHost(t)
	vk := filepath.Join(h.path("out"), "vk")
	assertCode(t, first(h.keyCmd("add", "base", "--for", "verifier", "--to-file", vk)), exitOK)
	code, _, stderr := h.keyCmd("remove", "base", h.verifierKeyID()[:12], "--yes")
	if code != exitOK {
		t.Fatalf("key remove: code %d, stderr %q", code, stderr)
	}
	if _, exit := h.resticAs(vk, "snapshots"); exit != 12 {
		t.Errorf("restic snapshots with the removed key ended with %d, want 12 (wrong password)", exit)
	}
	if _, exit := h.resticAs(h.path("secrets/base.pass"), "snapshots"); exit != 0 {
		t.Errorf("restic snapshots with the password of the host ended with %d", exit)
	}
}
