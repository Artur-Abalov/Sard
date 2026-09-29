// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// sink collects what the executor reports.
type sink struct {
	results  chan *agentv1.StepResult
	progress chan *agentv1.StepProgress
}

func newSink() *sink {
	return &sink{results: make(chan *agentv1.StepResult, 4), progress: make(chan *agentv1.StepProgress, 64)}
}

func (s *sink) Progress(p *agentv1.StepProgress) {
	select {
	case s.progress <- p:
	default:
	}
}
func (s *sink) Result(r *agentv1.StepResult) { s.results <- r }
func (*sink) Log(string, *agentv1.LogLine)   {}

func (s *sink) result(t *testing.T) *agentv1.StepResult {
	t.Helper()
	select {
	case r := <-s.results:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("no step result")
		return nil
	}
}

// The adapter behind the executor: a backup step ends SUCCEEDED with the
// snapshot and repository id, an invalid config REJECTED (strategy 5).
func TestTheExecutorRunsPluginStepsThroughTheHandlers(t *testing.T) {
	f := newHandlers(t, &plugin{})
	s := newSink()
	state := filepath.Join(t.TempDir(), "state") // the executor creates it 0700
	exec, err := executor.New(executor.Options{
		Handlers:     f.handlers,
		Sink:         s,
		StateDir:     state,
		Repositories: []string{"main"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Close(context.Background()) })

	exec.Submit(step(backup, `{}`))
	r := s.result(t)
	if r.GetStatus() != agentv1.StepStatus_STEP_STATUS_SUCCEEDED || r.GetBackup().GetRepositoryId() != "repo-id" || r.GetBackup().GetSnapshotId() != "snap" {
		t.Fatalf("result = %v", r)
	}

	bad := step(backup, `{"token": "mysql"}`)
	bad.CommandId = "cmd-2"
	exec.Submit(bad)
	if r := s.result(t); r.GetStatus() != agentv1.StepStatus_STEP_STATUS_REJECTED || r.GetOutput() != nil {
		t.Fatalf("result = %v", r)
	}
}
