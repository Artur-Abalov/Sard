// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// docs/specs/agent/agent-certificate-renewal.feature, tag @executor.

// blockedHandler holds every step it runs until the test releases it.
type blockedHandler struct {
	started chan struct{}
	release chan struct{}
}

func newBlockedHandler() *blockedHandler {
	return &blockedHandler{started: make(chan struct{}), release: make(chan struct{})}
}

func (b *blockedHandler) Actions() []agentv1.Action {
	return []agentv1.Action{agentv1.Action_ACTION_BACKUP}
}

func (b *blockedHandler) Run(ctx context.Context, _ *agentv1.RunStep, _ executor.Reporter) (*agentv1.StepResult, error) {
	close(b.started)
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return &agentv1.StepResult{}, nil
}

func (b *blockedHandler) Handler(string) (executor.Handler, bool) { return b, true }

// runStepQ starts the agent with a step Q that the test holds running.
func (h *renewalHost) runStepQ() (*runningAgent, *blockedHandler) {
	h.t.Helper()
	handler := newBlockedHandler()
	// A backup names a repository of the host.
	h.writeFile("qa.pass", []byte("pw"), 0o600)
	h.cfg = writeConfig(h.t, "server:\n  address: "+h.address+"\n"+
		"tls: {ca_file: "+h.path("ca.pem")+", cert_file: "+h.path("agent.pem")+", key_file: "+h.path("agent.key")+"}\n"+
		"executor: {state_dir: "+h.stateDir+"}\n"+withRestic(h.t)+
		"repositories:\n  - {name: qa, url: "+h.path("repo")+", password_file: "+h.path("qa.pass")+"}\n")
	h.server.steps = []*agentv1.RunStep{{CommandId: "Q", Plugin: "files", Action: agentv1.Action_ACTION_BACKUP, RepositoryName: "qa"}}
	h.handlers = handler
	a := h.run()
	select {
	case <-handler.started:
	case <-time.After(10 * time.Second):
		h.t.Fatalf("the step did not start; events = %v, results = %v, stderr = %q", h.server.eventLog(), h.server.results, a.stderr.String())
	}
	return a, handler
}

// renewDuringStep moves the clock to the threshold and waits for the renewal.
func (h *renewalHost) renewDuringStep() {
	h.t.Helper()
	h.renewalPause()
	h.clock.Advance(200 * 24 * time.Hour)
	h.waitLog("certificate renewed")
}

// journal is every file of the state dir with its content, as the executor left it.
func (h *renewalHost) journal() map[string]string {
	files := map[string]string{}
	_ = filepath.WalkDir(h.stateDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := os.ReadFile(path)
			files[path] = string(data)
		}
		return nil
	})
	return files
}

func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// Scenario: Шаг, начатый до продления, завершается и его результат уходит по тому же потоку
func TestAStepStartedBeforeTheRenewalFinishesAndReportsOnTheSameStream(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	a, handler := h.runStepQ()
	before := h.journal()
	h.renewDuringStep()
	if !sameFiles(before, h.journal()) {
		t.Fatalf("the journal changed during the renewal:\n%v\n%v", before, h.journal())
	}
	close(handler.release)
	h.server.waitCount(t, "result Q", 1)
	if h.server.count("connect") != 1 || h.server.count("register") != 1 || a.exited() {
		t.Fatalf("events = %v", h.server.eventLog())
	}
	r := h.server.results[0]
	if r.GetStatus() != agentv1.StepStatus_STEP_STATUS_SUCCEEDED || strings.Contains(r.GetMessage(), "interrupted") {
		t.Fatalf("result = %v", r)
	}
	h.requireAcknowledged()
}

// requireAcknowledged: the ack reached the executor, so the next stream neither
// lists Q nor sends its result again.
func (h *renewalHost) requireAcknowledged() {
	h.t.Helper()
	h.server.dropStreams()
	h.reconnect(2)
	h.server.waitCount(h.t, "hello", 2)
	if h.server.count("result Q") != 1 || len(h.server.hellos[1].GetRunningCommandIds()) != 0 {
		h.t.Fatalf("events = %v, hello = %v", h.server.eventLog(), h.server.hellos[1])
	}
}

// Scenario: Поток, оборвавшийся после продления во время шага, восстанавливается с новым сертификатом
func TestAStreamLostAfterARenewalDuringAStepRecoversWithTheNewCertificate(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	_, handler := h.runStepQ()
	h.renewDuringStep()
	h.server.dropStreams()
	h.reconnect(2)
	h.server.waitCount(t, "hello C2", 1)
	if !contains(h.server.eventLog(), "register C2") || len(h.server.hellos[1].GetRunningCommandIds()) != 1 || h.server.hellos[1].GetRunningCommandIds()[0] != "Q" {
		t.Fatalf("events = %v, hello = %v", h.server.eventLog(), h.server.hellos[1])
	}
	close(handler.release)
	h.server.waitCount(t, "result Q", 1)
	if h.server.results[0].GetStatus() != agentv1.StepStatus_STEP_STATUS_SUCCEEDED {
		t.Fatalf("result = %v", h.server.results[0])
	}
}
