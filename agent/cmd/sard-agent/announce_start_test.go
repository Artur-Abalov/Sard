// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Rule "repository id сравнивается с объявленным службой серверу" (@start,
// docs/specs/agent/host-check.feature, A8d-1).

func TestTheServiceRecordsTheRepositoriesItAnnouncesAtRegister(t *testing.T) {
	t.Parallel()
	h := newRefusalHost(t, unavailable)
	pass := filepath.Join(h.dir, "base.pass")
	writeFile(t, pass, []byte("x\n"), 0o600)
	script := resticAnswering(t, "REPO-A")
	h.cfg = writeConfig(t, "server:\n  address: "+h.address+"\n"+
		"tls: {ca_file: "+h.dir+"/ca.pem, cert_file: "+h.dir+"/agent.pem, key_file: "+h.dir+"/agent.key}\n"+
		"executor: {state_dir: "+h.dir+"/state}\nrestic: {path: "+script+", cache_dir: "+t.TempDir()+"}\n"+
		"repositories:\n  - {name: base, url: "+filepath.Join(h.dir, "base")+", password_file: "+pass+"}\n")
	h.runUntilRegistered(eventWait)
	state := filepath.Join(h.dir, "state")
	got, err := hostcheck.ReadAnnounced(os.ReadFile, state)
	if err != nil || len(got) != 1 || got[0].Name != "base" || got[0].RepositoryID != "REPO-A" {
		t.Fatalf("record %+v, %v", got, err)
	}
	assertOwnerOnlyWithoutTemporaryFiles(t, state)
}

func assertOwnerOnlyWithoutTemporaryFiles(t *testing.T, state string) {
	t.Helper()
	info, err := os.Stat(filepath.Join(state, hostcheck.AnnouncedFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file %v, %v", info, err)
	}
	entries, _ := os.ReadDir(state)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temporary file %s", e.Name())
		}
	}
}

func TestARecordThatCannotBeWrittenIsLoggedAndDoesNotStopTheRegister(t *testing.T) {
	t.Parallel()
	var logged strings.Builder
	record := recordAnnounced(filepath.Join(t.TempDir(), "missing"), slog.New(slog.NewTextHandler(&logged, nil)))
	record([]*agentv1.RepositoryInfo{{Name: "base", RepositoryId: "REPO-A"}})
	if !strings.Contains(logged.String(), "were not recorded") {
		t.Fatalf("log %q", logged.String())
	}
}
