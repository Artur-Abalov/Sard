// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
	"github.com/Artur-Abalov/sard/agent/internal/transport"
)

// docs/specs/agent/agent-certificate-renewal.feature: P18.

func TestAnAddressWithoutAPortIsNamedInTheError(t *testing.T) {
	r := newRig(t)
	files := r.ca.agentFiles(t, r.ca)
	err := transport.NewCertClient("no-port", files.CAFile).Confirm(context.Background(), files.CertFile, files.KeyFile)
	if err == nil || !strings.Contains(err.Error(), "server.address") {
		t.Fatalf("err = %v", err)
	}
}

func TestAMissingCAIsAFileErrorAndNoConnectionIsMade(t *testing.T) {
	r := newRig(t)
	files := r.ca.agentFiles(t, r.ca)
	missing := filepath.Join(t.TempDir(), "absent-ca.pem")
	called := false
	r.server.confirm = func(context.Context) error { called = true; return nil }
	err := transport.NewCertClient(r.server.addr, missing).Confirm(context.Background(), files.CertFile, files.KeyFile)
	var answer *certrenew.CallError
	if err == nil || !strings.Contains(err.Error(), "tls.ca_file") || errors.As(err, &answer) || called {
		t.Fatalf("err = %v, answered = %v, called = %v", err, answer, called)
	}
}

func TestAnAddressGrpcCannotDialIsADialError(t *testing.T) {
	r := newRig(t)
	files := r.ca.agentFiles(t, r.ca)
	err := transport.NewCertClient("%zz:443", files.CAFile).Confirm(context.Background(), files.CertFile, files.KeyFile)
	if err == nil || !strings.HasPrefix(err.Error(), "dial: ") {
		t.Fatalf("err = %v", err)
	}
}

func TestACallIsBoundedByThirtySeconds(t *testing.T) {
	r := newRig(t)
	files := r.ca.agentFiles(t, r.ca)
	var left time.Duration
	r.server.confirm = func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("the call has no deadline")
		}
		left = time.Until(deadline)
		return nil
	}
	if err := transport.NewCertClient(r.server.addr, files.CAFile).Confirm(context.Background(), files.CertFile, files.KeyFile); err != nil {
		t.Fatal(err)
	}
	if left <= 29*time.Second || left > 30*time.Second {
		t.Fatalf("the deadline the server saw = %v away, want 30s", left)
	}
}

func TestTheConnectionOfACallEndsWithTheCall(t *testing.T) {
	r := newRig(t)
	files := r.ca.agentFiles(t, r.ca)
	r.server.confirm = func(context.Context) error { return nil }
	if err := transport.NewCertClient(r.server.addr, files.CAFile).Confirm(context.Background(), files.CertFile, files.KeyFile); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.server.connEnded:
	case <-time.After(5 * time.Second):
		t.Fatal("the connection of the call is still open")
	}
}
