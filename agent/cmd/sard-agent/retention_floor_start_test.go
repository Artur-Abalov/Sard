// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
)

// docs/specs/agent/retention-forget.feature (L1a), rule "Нижний предел
// задаётся только оператором хоста в основном конфиге".

func floorConfig(t *testing.T, body string) config.Config {
	t.Helper()
	cfg, err := config.Parse([]byte("server:\n  address: s:1\nrepositories:\n  - {name: R, url: /r, password_file: /p}\n  - {name: Q, url: /q, password_file: /p}\n" + body))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func floorLog(t *testing.T, body string) string {
	t.Helper()
	var out strings.Builder
	logRetentionFloors(floorConfig(t, body), slog.New(slog.NewTextHandler(&out, nil)))
	return out.String()
}

func TestБезНастройкиВЖурналеВстроенныйПредел(t *testing.T) {
	got := floorLog(t, "")
	for _, want := range []string{
		"retention floor repository=R keep_last=7 keep_within_days=14 origin=built_in",
		"retention floor repository=Q keep_last=7 keep_within_days=14 origin=built_in",
	} {
		if !strings.Contains(got, want) || !strings.Contains(got, "level=INFO") {
			t.Errorf("log %q lacks %q", got, want)
		}
	}
}

func TestПределПоУмолчаниюИПределРепозиторияВЖурнале(t *testing.T) {
	got := floorLog(t, "retention_floor:\n  default: {keep_last: 10, keep_within_days: 30}\n  repositories:\n    R: {keep_within_days: 60}\n")
	for _, want := range []string{
		"retention floor repository=R keep_last=10 keep_within_days=60 origin=config_repository",
		"retention floor repository=Q keep_last=10 keep_within_days=30 origin=config_default",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log %q lacks %q", got, want)
		}
	}
}

func TestВыключенныйПределВЖурнале(t *testing.T) {
	got := floorLog(t, "retention_floor:\n  repositories:\n    R: {disabled: true}\n")
	if !strings.Contains(got, "retention floor repository=R disabled origin=config_repository") {
		t.Errorf("log %q", got)
	}
}

func TestПределДляНеизвестногоРепозиторияПредупреждает(t *testing.T) {
	got := floorLog(t, "retention_floor:\n  repositories:\n    old: {keep_last: 30}\n")
	if !strings.Contains(got, "level=WARN") || !strings.Contains(got, "retention_floor.repositories.old") {
		t.Errorf("log %q", got)
	}
}

func TestНедопустимыйПределОстанавливаетАгентаДоПодключения(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ section, want string }{
		"нули":  {"retention_floor:\n  repositories:\n    base: {keep_last: 0, keep_within_days: 0}\n", "retention_floor.repositories.base"},
		"дробь": {"retention_floor:\n  repositories:\n    base: {keep_within_days: 1.5}\n", "retention_floor.repositories.base.keep_within_days"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newRefusalHost(t, unavailable)
			h.cfg = writeConfig(t, "server:\n  address: "+h.address+"\ntls: {ca_file: "+h.dir+"/ca.pem, cert_file: "+h.dir+"/agent.pem, key_file: "+h.dir+"/agent.key}\n"+
				"executor: {state_dir: "+h.dir+"/state}\n"+h.restic+"repositories:\n  - {name: base, url: "+h.dir+"/base, password_file: "+h.dir+"/pass}\n"+c.section)
			code, _, stderr := h.run(eventWait)
			if code != 1 || !strings.Contains(stderr, c.want) || h.server.registry.Load() != 0 {
				t.Errorf("code %d, Register %d, stderr %q", code, h.server.registry.Load(), stderr)
			}
		})
	}
}

func TestПределВоФрагментеAgentDОстанавливаетАгентаДоПодключения(t *testing.T) {
	t.Parallel()
	h := newRefusalHost(t, unavailable)
	frag := filepath.Join(filepath.Dir(h.cfg), "agent.d", "repo-R.yaml")
	if err := os.MkdirAll(filepath.Dir(frag), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, frag, []byte("retention_floor:\n  default: {keep_last: 1}\n"), 0o600)
	code, _, stderr := h.run(eventWait)
	if code != 1 || !strings.Contains(stderr, frag) || !strings.Contains(stderr, "retention_floor") || h.server.registry.Load() != 0 {
		t.Errorf("code %d, Register %d, stderr %q", code, h.server.registry.Load(), stderr)
	}
}
