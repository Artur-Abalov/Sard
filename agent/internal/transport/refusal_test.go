// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Artur-Abalov/sard/agent/internal/transport"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// docs/specs/agent/agent-start-refusals.feature (OQ-001).

func reasoned(code codes.Code, msg, domain, reason string, metadata map[string]string) error {
	st, err := status.New(code, msg).WithDetails(&errdetails.ErrorInfo{Domain: domain, Reason: reason, Metadata: metadata})
	if err != nil {
		panic(err)
	}
	return st.Err()
}

// Scenario: Отказ INVALID_ARGUMENT после здорового потока прекращает переподключение
func TestRefusalInvalidArgumentAfterAHealthyStreamStopsReconnecting(t *testing.T) {
	r := newRig(t)
	var calls atomic.Int32
	r.server.register = func() (*agentv1.RegisterResponse, error) {
		if calls.Add(1) == 1 {
			return registered("a", beat), nil
		}
		return nil, reasoned(codes.InvalidArgument, "register rejected", "sard.dev", "NAME_DUPLICATE", nil)
	}
	done := r.run(context.Background())
	ss := r.server.next(t)
	ss.read(t)
	r.clock.Advance(transport.HealthyStream + time.Second)
	close(ss.done)
	r.backoff(t, beat)
	timersBefore := len(r.clock.added)
	r.clock.Advance(time.Hour)
	err := wait(t, done)
	if err == nil || !transport.IsPermanent(err) {
		t.Fatalf("err = %v, want a permanent refusal", err)
	}
	if r.server.registerCount() != 2 || r.server.connectCount() != 1 {
		t.Fatalf("Register = %d, Connect = %d", r.server.registerCount(), r.server.connectCount())
	}
	if n := len(r.clock.added) - timersBefore; n != 0 {
		t.Errorf("%d timers were set after the refusal", n)
	}
	if strings.Count(r.logs.all(), "INFO reconnecting") != 1 {
		t.Errorf("log:\n%s", r.logs.all())
	}
}

const refusalSuffix = "not reconnecting: fix the cause, then restart the agent"

func refusalLine(t *testing.T, err error) string {
	t.Helper()
	r := newRig(t)
	r.server.register = func() (*agentv1.RegisterResponse, error) { return nil, err }
	got := wait(t, r.connect(context.Background()))
	if !transport.IsPermanent(got) {
		t.Fatalf("err = %v, want a permanent refusal", got)
	}
	return transport.RefusalLine(got)
}

// Scenario: Отказ Register с причиной сервера завершает агента с кодом 78
func TestRefusalLineShowsTheCodeTheReasonAndSortedMetadata(t *testing.T) {
	line := refusalLine(t, reasoned(codes.InvalidArgument, "register rejected", "sard.dev", "SNAPSHOT_TOO_LARGE",
		map[string]string{"limit": "256", "field": "repositories"}))
	want := "code = InvalidArgument desc = register rejected, reason SNAPSHOT_TOO_LARGE, field=repositories, limit=256; " + refusalSuffix
	if line != want {
		t.Fatalf("line = %q, want %q", line, want)
	}
}

// Scenario: Отказ Register без ErrorInfo тоже завершает агента с кодом 78
func TestRefusalLineWithoutErrorInfoHasNoReason(t *testing.T) {
	for _, code := range []codes.Code{codes.InvalidArgument, codes.FailedPrecondition, codes.Unauthenticated, codes.PermissionDenied} {
		line := refusalLine(t, status.Error(code, "refused-marker"))
		if !strings.Contains(line, "code = "+code.String()) || !strings.Contains(line, "refused-marker") ||
			strings.Contains(line, "reason ") || !strings.HasSuffix(line, refusalSuffix) {
			t.Errorf("%v: line = %q", code, line)
		}
	}
}

// Scenario: ErrorInfo чужого домена не выдаётся за причину сервера
func TestRefusalLineIgnoresErrorInfoOfAnotherDomain(t *testing.T) {
	line := refusalLine(t, reasoned(codes.InvalidArgument, "register rejected", "other.example", "NAME_DUPLICATE", nil))
	if strings.Contains(line, "NAME_DUPLICATE") {
		t.Fatalf("line = %q", line)
	}
}

// Scenario: Управляющие символы в ответе сервера не разрывают строку отказа
func TestRefusalLineEscapesControlCharacters(t *testing.T) {
	line := refusalLine(t, reasoned(codes.InvalidArgument, "bad\ntext", "sard.dev", "BAD\nREASON", map[string]string{"field": "x\ny"}))
	if strings.ContainsAny(line, "\n\r") || !strings.Contains(line, `reason BAD\nREASON, field=x\ny`) || !strings.Contains(line, `desc = bad\ntext`) {
		t.Fatalf("line = %q", line)
	}
}

// Scenario: Прочий код ответа на Register повторяется после задержки
func TestOtherRegisterCodesAreRetriedAfterADelay(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.Internal, codes.Unknown, codes.DeadlineExceeded,
		codes.ResourceExhausted, codes.Aborted, codes.Unimplemented} {
		r := newRig(t)
		var calls atomic.Int32
		r.server.register = func() (*agentv1.RegisterResponse, error) {
			if calls.Add(1) == 1 {
				return nil, status.Error(code, "later")
			}
			return registered("a", beat), nil
		}
		done := r.run(context.Background())
		r.clock.Advance(r.backoff(t, beat))
		if r.server.next(t).read(t).GetHello() == nil || r.server.registerCount() != 2 {
			t.Errorf("%v: the agent did not register again", code)
		}
		select {
		case err := <-done:
			t.Errorf("%v: Run ended: %v", code, err)
		default:
		}
	}
}

// Scenario: Поток, закрытый сервером с любым кодом, ведёт к переподключению
func TestAStreamClosedWithAnyCodeIsReconnected(t *testing.T) {
	closers := map[string]error{
		"UNAVAILABLE":         reasoned(codes.Unavailable, "x", "sard.dev", "SESSION_EXPIRED", nil),
		"FAILED_PRECONDITION": reasoned(codes.FailedPrecondition, "x", "sard.dev", "HELLO_REQUIRED", nil),
		"ALREADY_EXISTS":      reasoned(codes.AlreadyExists, "x", "sard.dev", "AGENT_DUPLICATE_SESSION", nil),
		"INVALID_ARGUMENT":    status.Error(codes.InvalidArgument, "x"),
		"UNAUTHENTICATED":     status.Error(codes.Unauthenticated, "x"),
		"PERMISSION_DENIED":   status.Error(codes.PermissionDenied, "x"),
	}
	for name, closeWith := range closers {
		r := newRig(t)
		r.server.closeStream = closeWith
		done := r.run(context.Background())
		ss := r.server.next(t)
		ss.read(t)
		close(ss.done)
		r.clock.Advance(r.backoff(t, beat))
		r.server.next(t)
		if n := r.server.registerCount(); n != 2 {
			t.Errorf("%s: Register calls = %d", name, n)
		}
		select {
		case err := <-done:
			t.Errorf("%s: Run ended: %v", name, err)
		default:
		}
	}
}
