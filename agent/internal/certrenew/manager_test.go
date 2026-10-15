// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// docs/specs/agent/agent-certificate-renewal.feature.

// Scenario: Агент не продлевает сертификат раньше двух третей срока
func TestTheAgentDoesNotRenewBeforeTwoThirdsOfTheLifetime(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.start()
	if d := e.idle(); d != 200*24*time.Hour {
		t.Fatalf("the first wait = %v, want 200 days", d)
	}
	e.clock.Advance(200*24*time.Hour - time.Second)
	if n := len(e.server.callsOf("renew")); n != 0 {
		t.Fatalf("RenewCertificate calls = %d", n)
	}
}

// Scenario: На двух третях срока агент запрашивает сертификат на новом ключе
func TestAtTwoThirdsOfTheLifetimeTheAgentAsksForACertificateOnANewKey(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.start()
	e.idle()
	e.clock.Advance(200 * 24 * time.Hour)
	if got := e.server.next(); got != "renew" {
		t.Fatalf("first call = %s", got)
	}
	calls := e.server.callsOf("renew")
	if len(calls) != 1 || calls[0].presented != "1" {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].pub.Curve.Params().Name != "P-256" || calls[0].pub.Equal(e.p1.Key.Public()) {
		t.Fatalf("the CSR key is %v", calls[0].pub.Curve.Params().Name)
	}
}

// renewNow runs the agent to the threshold and waits for the first outcome line.
func (e *env) renewNow(outcome string) {
	e.t.Helper()
	e.start()
	e.idle()
	e.clock.Advance(200 * 24 * time.Hour)
	e.logs.waitFor(e.t, outcome)
}

// Scenario: После подтверждения новая пара становится основной
func TestAfterTheConfirmationTheNewPairBecomesTheCurrentOne(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.renewNow("certificate renewed")
	issued := e.server.issued[0]
	if e.read("agent.pem") != issued || e.names() != "agent.key agent.pem ca.pem" {
		t.Fatalf("files = %s", e.names())
	}
	if strings.Contains(e.read("agent.key"), string(e.p1.KeyPEM)) || e.read("agent.key") == string(e.p1.KeyPEM) {
		t.Fatal("the old key is still there")
	}
	for name, mode := range map[string]os.FileMode{"agent.key": 0o600, "agent.pem": 0o644} {
		if info, err := os.Stat(e.path(name)); err != nil || info.Mode().Perm() != mode {
			t.Errorf("%s: %v, %v", name, info, err)
		}
	}
	line := e.logs.waitFor(t, "certificate renewed")
	if !strings.Contains(line, "old_serial=1") || !strings.Contains(line, "new_serial=2") || !strings.Contains(line, "not_after=2028-02-27T12:00:00Z") {
		t.Errorf("log = %q", line)
	}
}

// Scenario: Подтверждение идёт новой парой по отдельному соединению
func TestTheConfirmationIsMadeWithTheNewPair(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.renewNow("certificate renewed")
	confirms := e.server.callsOf("confirm")
	if len(confirms) != 1 || confirms[0].presented != "2" {
		t.Fatalf("confirmations = %+v", confirms)
	}
}

// Scenario: Порог считается от NotBefore по часам агента
func TestTheThresholdIsReadFromTheCertificate(t *testing.T) {
	t.Parallel()
	for life, threshold := range map[time.Duration]time.Duration{
		10 * time.Minute:     6*time.Minute + 40*time.Second,
		365 * 24 * time.Hour: 243*24*time.Hour + 8*time.Hour,
		30 * 24 * time.Hour:  20 * 24 * time.Hour,
	} {
		e := newEnvFor(t, t0.Add(life))
		e.start()
		if d := e.idle(); d != threshold {
			t.Errorf("lifetime %v: waits %v, want %v", life, d, threshold)
		}
		e.clock.Advance(threshold - time.Second)
		if n := len(e.server.callsOf("renew")); n != 0 {
			t.Errorf("lifetime %v: %d calls a second before the threshold", life, n)
		}
		e.clock.Advance(time.Second)
		e.server.next()
	}
}

// Scenario: Агент, запущенный после порога, продлевает сразу после регистрации
func TestAnAgentStartedAfterTheThresholdRenewsRightAfterRegistering(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.clock.Advance(250 * 24 * time.Hour)
	e.mgr = e.manager()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.mgr.Run(ctx)
	e.mgr.Registered()
	if got := e.server.next(); got != "renew" {
		t.Fatalf("call = %s", got)
	}
}

// Scenario: Следующее продление планируется по новому сертификату
func TestTheNextRenewalIsPlannedByTheNewCertificate(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.renewNow("certificate renewed")
	if d := e.idle(); d != 200*24*time.Hour {
		t.Fatalf("the next wait = %v, want 200 days", d)
	}
	e.clock.Advance(200*24*time.Hour - time.Second)
	if n := len(e.server.callsOf("renew")); n != 1 {
		t.Fatalf("RenewCertificate calls = %d", n)
	}
}
