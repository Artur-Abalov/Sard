// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
)

func TestAStoppedAgentThatNeverRegisteredEndsTheRenewal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.manager().Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}

func TestWithoutACertificateTheRenewalFailsAndTriesAgainLater(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if err := os.Remove(e.path("agent.pem")); err != nil {
		t.Fatal(err)
	}
	e.start()
	if d := e.idle(); d != time.Hour {
		t.Fatalf("wait = %v", d)
	}
	e.clock.Advance(time.Hour)
	e.logs.waitFor(t, "certificate renewal failed")
}

func TestAFailedRenameKeepsTheNewPairForTheNextAttempt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fs = failingFS{failRename: true, dir: e.dir}
	e.renewNow("certificate renewal failed")
	if e.names() != "agent.key agent.key.new agent.pem agent.pem.new ca.pem" {
		t.Fatalf("files = %s", e.names())
	}
	e.clock.Advance(e.idle())
	e.logs.waitFor(t, "failures=2")
	if n, c := len(e.server.callsOf("renew")), len(e.server.callsOf("confirm")); n != 1 || c != 2 {
		t.Fatalf("renewals = %d, confirmations = %d", n, c)
	}
}

func TestAMissingCAFileFailsTheAttemptWithoutWritingAnything(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if err := os.Remove(e.path("ca.pem")); err != nil {
		t.Fatal(err)
	}
	e.renewNow("certificate renewal failed")
	if e.names() != "agent.key agent.pem" {
		t.Fatalf("files = %s", e.names())
	}
	e.write("ca.pem", []byte("not a certificate"))
	e.clock.Advance(e.idle())
	e.logs.waitFor(t, "failures=2")
	if e.names() != "agent.key agent.pem ca.pem" {
		t.Fatalf("files = %s", e.names())
	}
}

func TestTheDefaultsAreTheProductionOnes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.startWith(certrenew.New(certrenew.Options{Files: e.files(), StatePath: e.statePath(), FS: e.fs, Caller: e.server, Clock: e.clock}))
	e.mgr.Registered()
	e.mgr.Registered()
	e.idle()
	e.clock.Advance(200 * 24 * time.Hour)
	for e.server.next() != "confirm" {
	}
	if n := len(e.server.callsOf("renew")); n != 1 {
		t.Fatalf("renewals = %d", n)
	}
}
