// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// recorder is a slog.Handler that keeps every record as "msg k=v k=v".
type recorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *recorder) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *recorder) WithGroup(string) slog.Handler            { return r }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", rec.Level, rec.Message)
	rec.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, b.String())
	return nil
}

func (r *recorder) all() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

// wait waits for a line that contains want: records come from the
// transport's goroutines.
func (r *recorder) wait(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(r.all(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("no log line with %q; log:\n%s", want, r.all())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

const secretMessage = "password=hunter2-in-a-result"

// Scenario: Обрыв связи, переподключение, Hello и подтверждения видны в журнале агента
func TestTheLogTellsConnectionsHelloResultsAcksAndReconnects(t *testing.T) {
	r := newRig(t)
	r.state.running = []string{"run-1", "run-2"}
	r.state.pending = []*agentv1.StepResult{{CommandId: "r1", Status: agentv1.StepStatus_STEP_STATUS_FAILED, Message: secretMessage}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.run(ctx)
	ss := r.server.next(t)
	ss.read(t)
	ss.read(t)
	r.logs.wait(t, "INFO connected to the server heartbeat=30s")
	r.logs.wait(t, "INFO hello sent running=2 pending_results=1")
	r.logs.wait(t, "INFO result sent command_id=r1 status=STEP_STATUS_FAILED")

	ss.send <- &agentv1.ConnectResponse{Message: &agentv1.ConnectResponse_ResultAck{ResultAck: &agentv1.ResultAck{CommandId: "r1"}}}
	r.logs.wait(t, "INFO result acknowledged command_id=r1")

	close(ss.done)
	r.logs.wait(t, "INFO connection to the server lost code=")
	r.logs.wait(t, "INFO reconnecting attempt=1 delay=1s")
	if strings.Contains(r.logs.all(), "hunter2") {
		t.Fatalf("a result message reached the log:\n%s", r.logs.all())
	}
}

func TestTheLogNamesTheCodeOfARefusedRegister(t *testing.T) {
	r := newRig(t)
	r.server.register = func() (*agentv1.RegisterResponse, error) { return nil, status.Error(codes.Unavailable, "restarting") }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.run(ctx)
	r.logs.wait(t, "INFO connection to the server lost code=Unavailable")
}

func TestAPermanentRefusalIsLoggedAsAnError(t *testing.T) {
	r := newRig(t)
	r.server.register = func() (*agentv1.RegisterResponse, error) { return nil, status.Error(codes.PermissionDenied, "revoked") }
	if err := wait(t, r.run(context.Background())); err == nil {
		t.Fatal("Run returned nil")
	}
	r.logs.wait(t, "ERROR the server refused the agent; not reconnecting")
}
