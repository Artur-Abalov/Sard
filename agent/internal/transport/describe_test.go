// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport_test

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/Artur-Abalov/sard/agent/internal/transport"
)

// docs/specs/agent/agent-certificate-renewal.feature, P25.

func TestDescribeNamesTheCodeAndTheServersReason(t *testing.T) {
	t.Parallel()
	rows := []struct {
		err  error
		want string
	}{
		{reasoned(codes.Unavailable, "down", "sard.dev", "INTERNAL_RETRYABLE", nil), "Unavailable INTERNAL_RETRYABLE"},
		{status.Error(codes.Internal, "down"), "Internal"},
		{reasoned(codes.Internal, "down", "other.example", "X", nil), "Internal"},
		{reasoned(codes.InvalidArgument, "bad", "sard.dev", "BAD\nREASON", nil), `InvalidArgument BAD\nREASON`},
		{errors.New("write /d/k: disk\nfull"), `write /d/k: disk\nfull`},
	}
	for _, row := range rows {
		if got := transport.Describe(row.err); got != row.want {
			t.Errorf("Describe(%v) = %q, want %q", row.err, got, row.want)
		}
	}
}

func TestRefusalReasonIsTheServersReasonOrEmpty(t *testing.T) {
	t.Parallel()
	if got := transport.RefusalReason(reasoned(codes.Unauthenticated, "x", "sard.dev", "CERT_EXPIRED", nil)); got != "CERT_EXPIRED" {
		t.Errorf("reason = %q", got)
	}
	if got := transport.RefusalReason(status.Error(codes.Unauthenticated, "x")); got != "" {
		t.Errorf("reason = %q", got)
	}
}

func TestRetryDelayIsTheServersRetryInfo(t *testing.T) {
	t.Parallel()
	st, err := status.New(codes.ResourceExhausted, "slow down").WithDetails(&errdetails.RetryInfo{RetryDelay: durationpb.New(40 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if got := transport.RetryDelay(st.Err()); got != 40*time.Minute {
		t.Errorf("delay = %v", got)
	}
	if got := transport.RetryDelay(status.Error(codes.ResourceExhausted, "x")); got != 0 {
		t.Errorf("delay = %v", got)
	}
	if got := transport.RetryDelay(errors.New("x")); got != 0 {
		t.Errorf("delay = %v", got)
	}
}

func TestRefusalLineIsTheDetailAndTheSuffix(t *testing.T) {
	t.Parallel()
	err := reasoned(codes.Unauthenticated, "expired", "sard.dev", "CERT_EXPIRED", nil)
	detail := transport.RefusalDetail(err)
	if detail != "code = Unauthenticated desc = expired, reason CERT_EXPIRED" || transport.RefusalLine(err) != detail+"; "+transport.RefusalSuffix {
		t.Fatalf("detail = %q, line = %q", detail, transport.RefusalLine(err))
	}
}

func TestBackoffIsTheReconnectDelay(t *testing.T) {
	t.Parallel()
	rows := []struct {
		attempt int
		rnd     float64
		want    time.Duration
	}{{0, 1, time.Second}, {1, 0.5, time.Second}, {5, 1, 32 * time.Second}, {6, 1, time.Minute}, {40, 1, time.Minute}}
	for _, row := range rows {
		if got := transport.Backoff(row.attempt, func() float64 { return row.rnd }); got != row.want {
			t.Errorf("Backoff(%d, %v) = %v, want %v", row.attempt, row.rnd, got, row.want)
		}
	}
}
