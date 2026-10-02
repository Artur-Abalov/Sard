// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:build integration

package pluginhost_test

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/crypto"
	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	"github.com/Artur-Abalov/sard/agent/internal/restic"
	"github.com/Artur-Abalov/sard/agent/internal/stepsecrets"
	filesplugin "github.com/Artur-Abalov/sard/agent/plugins/files"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// logSink records log lines and results, as the transport would get them.
type logSink struct {
	mu      sync.Mutex
	lines   []*agentv1.LogLine
	results chan *agentv1.StepResult
}

func (*logSink) Progress(*agentv1.StepProgress) {}
func (s *logSink) Result(r *agentv1.StepResult) { s.results <- r }
func (s *logSink) Log(_ string, l *agentv1.LogLine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, l)
}

// A7c, strategy 1 for restic's stderr: the pinned restic reports a
// repository whose path contains an agent secret. Neither the step's log
// lines nor its result carry the value; the lines carry the marker.
func TestResticStderrReachesTheStepLogMasked(t *testing.T) {
	const value = "hunter2-very-secret"
	exec, sink, tmp := maskingAgent(t, value, value)
	exec.Submit(&agentv1.RunStep{
		CommandId: "c1", Plugin: "files", Action: agentv1.Action_ACTION_BACKUP, RepositoryName: "main",
		ConfigJson: `{"paths":["` + tmp + `"]}`,
	})
	r := <-sink.results
	if r.GetStatus() != agentv1.StepStatus_STEP_STATUS_FAILED || strings.Contains(r.GetMessage(), value) {
		t.Fatalf("result = %v", r)
	}
	sink.wantMasked(t, value)
}

// A7b: restic reports the repository path with the base64url of the secret
// (padded); the marker replaces it and the value is nowhere.
func TestBase64OfASecretInResticStderrIsMasked(t *testing.T) {
	const value = "hunter2-very-secret?"
	exec, sink, tmp := maskingAgent(t, value, base64.URLEncoding.EncodeToString([]byte(value)))
	exec.Submit(&agentv1.RunStep{
		CommandId: "c1", Plugin: "files", Action: agentv1.Action_ACTION_BACKUP, RepositoryName: "main",
		ConfigJson: `{"paths":["` + tmp + `"]}`,
	})
	r := <-sink.results
	if r.GetStatus() != agentv1.StepStatus_STEP_STATUS_FAILED || strings.Contains(r.GetMessage(), value) {
		t.Fatalf("result = %v", r)
	}
	sink.wantMasked(t, value)
	sink.wantMasked(t, base64.URLEncoding.EncodeToString([]byte(value)))
}

// maskingAgent wires the files plugin, the pinned restic and the executor
// as main does; the repository's path, which does not exist, holds value,
// and so does the agent's secret "token".
func maskingAgent(t *testing.T, value, inPath string) (*executor.Executor, *logSink, string) {
	t.Helper()
	tmp := t.TempDir()
	repo := config.Repository{Name: "main", URL: filepath.Join(tmp, "repo-"+inPath), PasswordFile: secretFile(t, tmp, "password", "integration\n")}
	cfg := config.Config{Secrets: map[string]string{"token": secretFile(t, tmp, "token", value+"\n")}, Repositories: []config.Repository{repo}}
	cli := restic.New(restic.Options{
		Binary:   pinnedRestic(t),
		CacheDir: filepath.Join(tmp, "cache"),
		Path:     os.Getenv("PATH"),
		Exec:     restic.ProcessExecutor{},
		Keys:     crypto.NewResticAES(cfg.PasswordFiles()),
		ReadFile: os.ReadFile,
	}, repo)
	reg, err := sdk.NewRegistry(filesplugin.Plugin{AgentVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	handlers, err := pluginhost.NewHandlers(reg, pluginhost.NewSecrets(cfg.Secrets, os.ReadFile),
		func(name string, stderr io.Writer) (restic.Repository, bool) {
			return cli.WithStderr(stderr), name == "main"
		},
		filepath.Join(tmp, "restore"))
	if err != nil {
		t.Fatal(err)
	}
	sink := &logSink{results: make(chan *agentv1.StepResult, 1)}
	exec, err := executor.New(executor.Options{
		Handlers:     handlers,
		Sink:         sink,
		StateDir:     filepath.Join(tmp, "state"),
		Repositories: []string{"main"},
		Secrets:      stepsecrets.New(cfg, os.ReadFile, slog.New(slog.NewTextHandler(io.Discard, nil))),
		OutputLevel:  pluginhost.OutputLevel,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Close(context.Background()) })
	return exec, sink, tmp
}

func secretFile(t *testing.T, dir, name, data string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// wantMasked fails on a line carrying value or without a masked warning.
func (s *logSink) wantMasked(t *testing.T, value string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	masked := false
	for _, l := range s.lines {
		t.Logf("%v %s", l.GetLevel(), l.GetText())
		if strings.Contains(l.GetText(), value) {
			t.Fatalf("leaked: %q", l.GetText())
		}
		masked = masked || (strings.Contains(l.GetText(), "[REDACTED]") && l.GetLevel() == agentv1.LogLevel_LOG_LEVEL_WARN)
	}
	if !masked {
		t.Fatalf("no masked warning among %d lines", len(s.lines))
	}
}
