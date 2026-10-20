// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// docs/specs/agent/agent-certificate-renewal.feature: P23.

func refusedAs(t *testing.T, code codes.Code, reason string) error {
	t.Helper()
	st, err := status.New(code, "refused").WithDetails(&errdetails.ErrorInfo{Domain: sardErrorDomain, Reason: reason})
	if err != nil {
		t.Fatal(err)
	}
	if code == codes.Unauthenticated {
		return fmt.Errorf("register: %w: %w", ErrNotAuthorized, st.Err())
	}
	return fmt.Errorf("register: %w", st.Err())
}

// Scenario: Отказ CERT_SUPERSEDED при годной новой паре переводит агента на неё
func TestASpareIsAskedForOnlyWhenTheServerSaysSuperseded(t *testing.T) {
	t.Parallel()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	rows := []struct {
		name   string
		ctx    context.Context
		err    error
		hook   bool // a hook is set
		answer bool
		want   bool
		asked  int
	}{
		{"superseded and a spare", context.Background(), refusedAs(t, codes.Unauthenticated, "CERT_SUPERSEDED"), true, true, true, 1},
		{"superseded and no spare", context.Background(), refusedAs(t, codes.Unauthenticated, "CERT_SUPERSEDED"), true, false, false, 1},
		{"no hook", context.Background(), refusedAs(t, codes.Unauthenticated, "CERT_SUPERSEDED"), false, true, false, 0},
		{"stopped agent", cancelled, refusedAs(t, codes.Unauthenticated, "CERT_SUPERSEDED"), true, true, false, 0},
		{"another reason", context.Background(), refusedAs(t, codes.Unauthenticated, "AGENT_REVOKED"), true, true, false, 0},
		{"no reason", context.Background(), fmt.Errorf("register: %w: %w", ErrNotAuthorized, status.Error(codes.Unauthenticated, "x")), true, true, false, 0},
		{"the reason on a refusal that is no authorization", context.Background(), refusedAs(t, codes.Unavailable, "CERT_SUPERSEDED"), true, true, false, 0},
		{"no error", context.Background(), nil, true, true, false, 0},
	}
	for _, row := range rows {
		asked := 0
		tr := &Transport{}
		if row.hook {
			tr.opts.OnSuperseded = func(context.Context) bool { asked++; return row.answer }
		}
		if got := tr.trySpare(row.ctx, row.err); got != row.want || asked != row.asked {
			t.Errorf("%s: spare = %v, asked = %d", row.name, got, asked)
		}
	}
}
