// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
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

type silentReporter struct{ executor.Reporter }

func (silentReporter) Log(agentv1.LogLevel, string) {}
func (silentReporter) Output() io.Writer            { return io.Discard }

// The agent runs FORGET with its own repositories and the floor of its config.
func TestTheAgentRunsForgetStepsOverTheRepositoriesOfItsConfig(t *testing.T) {
	script, calls := recordingRestic(t)
	cfg, err := config.Parse([]byte("server:\n  address: s:1\nrepositories:\n  - {name: main, url: /srv/main, password_file: /etc/sard/main.pass}\n"))
	if err != nil {
		t.Fatal(err)
	}
	agent := buildAgent(cfg, "maint", script, "", nil, nil, false)
	step := &agentv1.RunStep{CommandId: "c1", Action: agentv1.Action_ACTION_FORGET, RepositoryName: "main",
		Forget: &agentv1.ForgetTask{SourceId: "s1", Policy: &agentv1.RetentionPolicy{KeepLast: 1}}}
	_, err = agent.Forget.Run(t.Context(), step, silentReporter{})
	if err == nil || errors.Is(err, executor.ErrRejected) {
		t.Fatalf("err = %v: the fake restic prints no snapshot list, so the step fails after listing", err)
	}
	if lines := calls(); len(lines) != 1 || !strings.Contains(lines[0], "snapshots --json --no-lock --tag sard.source=s1") {
		t.Errorf("restic was run as %q", lines)
	}
}

// A FORGET step goes through the started agent: stream, role gate, executor,
// the retention handler over a fake restic, and its ForgetOutput comes back.
func TestAForgetStepSentByTheServerReachesTheRetentionHandlerAndItsOutputIsReported(t *testing.T) {
	h := newAgentHost(t)
	installPair(t, h.dir, withRole("source")...)
	writeFile(t, filepath.Join(h.dir, "qa.pass"), []byte("pw"), 0o600)
	h.extra = "repositories:\n  - {name: qa, url: " + h.dir + "/repo, password_file: " + h.dir + "/qa.pass}\n" +
		"retention_floor:\n  repositories:\n    qa: {disabled: true}\n"
	a, b, c := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	snap := func(id, day string) string {
		return `{"id":"` + id + `","time":"2026-11-` + day + `T10:00:00Z","hostname":"h","tags":["sard.source=s1"]}`
	}
	done := filepath.Join(h.dir, "forgotten")
	script := "#!/bin/sh\necho \"$1\" >> " + h.resticCalls + "\n" +
		"case \"$1\" in\n" +
		"snapshots) if [ -e " + done + " ]; then echo '[" + snap(c, "30") + "]'; else echo '[" + snap(a, "28") + "," + snap(b, "29") + "," + snap(c, "30") + "]'; fi ;;\n" +
		"forget) touch " + done + " ;;\n" +
		"unlock) ;;\n" +
		"*) echo 'restic 0.19.1 compiled with go1.26.4 on linux/amd64' ;;\nesac\n"
	h.restic = "restic: {path: " + writeScript(t, filepath.Join(h.dir, "restic"), script) + "}\n"
	h.server.steps = []*agentv1.RunStep{{CommandId: "f1", Action: agentv1.Action_ACTION_FORGET, RepositoryName: "qa",
		Forget: &agentv1.ForgetTask{SourceId: "s1", Policy: &agentv1.RetentionPolicy{KeepLast: 1}}}}
	code, _, stderr := h.runUntil(15*time.Second, []string{"--config", h.config()}, func() bool { return len(h.server.stepResults()) > 0 })
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	requireForgetOutput(t, h.server.stepResults()[0], []string{a, b}, []string{c})
}

func requireForgetOutput(t *testing.T, r *agentv1.StepResult, forgotten, remaining []string) {
	t.Helper()
	out := r.GetForget()
	if r.GetCommandId() != "f1" || r.GetStatus() != agentv1.StepStatus_STEP_STATUS_SUCCEEDED || out == nil {
		t.Fatalf("result = %v", r)
	}
	if !slices.Equal(out.GetForgottenSnapshotIds(), forgotten) || !slices.Equal(out.GetRemainingSnapshotIds(), remaining) || out.GetListedAt() == nil {
		t.Errorf("output = %v", out)
	}
}
