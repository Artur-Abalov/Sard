// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
	"github.com/Artur-Abalov/sard/agent/internal/certtest"
)

// docs/specs/agent/agent-certificate-renewal.feature: P21, confirmation at start,
// and P23, CERT_SUPERSEDED.

// withNewPair puts pair 2 (serial 2, issued at T0 plus 200 days) next to pair 1.
func (e *env) withNewPair() certtest.Pair {
	p2 := e.ca.NewPair(e.t, certtest.Spec{Tenant: tenant, Agent: agent, Role: "source", Serial: 2, NotBefore: day(200), NotAfter: day(500)})
	e.write("agent.key.new", p2.KeyPEM)
	e.write("agent.pem.new", p2.CertPEM)
	e.clock.Set(day(201))
	return p2
}

func (e *env) selection() certpair.Selection {
	e.t.Helper()
	sel, err := certpair.Select(e.files(), e.fs, e.clock.Now())
	if err != nil || !sel.Pending() {
		e.t.Fatalf("selection = %+v, err = %v", sel, err)
	}
	return sel
}

// Scenario: Перезапуск после записи новой пары до подтверждения
func TestAdoptConfirmsTheNewPairAndRenamesIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p2 := e.withNewPair()
	if err := e.manager().Adopt(context.Background(), e.selection()); err != nil {
		t.Fatal(err)
	}
	confirms := e.server.callsOf("confirm")
	if len(confirms) != 1 || confirms[0].presented != "2" || len(e.server.callsOf("renew")) != 0 {
		t.Fatalf("calls = %+v", e.server.calls)
	}
	if e.names() != "agent.key agent.pem ca.pem" || e.read("agent.key") != string(p2.KeyPEM) || e.read("agent.pem") != string(p2.CertPEM) {
		t.Fatalf("files = %s", e.names())
	}
}

// Scenario: Сервер недоступен при подтверждении на старте, и агент не регистрируется старой парой
func TestAdoptRepeatsATemporaryRefusalAtTheReconnectDelays(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.rnd = 1
	e.withNewPair()
	e.server.confirm = func(n int) error {
		if n <= 3 {
			return status.Error(codes.Unavailable, "down")
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- e.manager().Adopt(context.Background(), e.selection()) }()
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		if d := e.idle(); d != want {
			t.Fatalf("pause = %v, want %v", d, want)
		}
		e.clock.Advance(want)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if n := len(e.server.callsOf("confirm")); n != 4 {
		t.Fatalf("confirmations = %d", n)
	}
}

// Scenario: Окончательный отказ в подтверждении на старте возвращает агента к текущей паре
func TestAdoptRemovesTheNewPairOnAFinalRefusal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.withNewPair()
	e.server.confirm = func(int) error { return reasoned(codes.Unauthenticated, "CERT_REVOKED") }
	err := e.manager().Adopt(context.Background(), e.selection())
	if !errors.Is(err, certrenew.ErrDiscarded) || e.names() != "agent.key agent.pem ca.pem" || e.read("agent.key") != string(e.p1.KeyPEM) {
		t.Fatalf("err = %v, files = %s", err, e.names())
	}
}

func TestAdoptStopsWhenTheAgentIsStopped(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.withNewPair()
	e.server.confirm = func(int) error { return status.Error(codes.Unavailable, "down") }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- e.manager().Adopt(ctx, e.selection()) }()
	e.idle()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if !e.exists("agent.key.new") {
		t.Error("the new pair was removed")
	}
}

// Scenario: Отказ CERT_SUPERSEDED при годной новой паре переводит агента на неё
func TestSupersededAdoptsAnewPairWrittenNextToTheCurrentOne(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	p2 := e.withNewPair()
	if !e.manager().Superseded(context.Background()) {
		t.Fatal("Superseded = false, want to reconnect with the new pair")
	}
	if e.read("agent.pem") != string(p2.CertPEM) || len(e.server.callsOf("confirm")) != 1 {
		t.Fatalf("files = %s", e.names())
	}
}

// Scenario: Отказ CERT_SUPERSEDED без другой пары завершает агента с командой перерегистрации
func TestSupersededWithoutASpareIsFinal(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if e.manager().Superseded(context.Background()) {
		t.Fatal("Superseded = true without another pair")
	}
}

func TestSupersededIsFinalWhenTheSpareIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.withNewPair()
	e.server.confirm = func(int) error { return reasoned(codes.Unauthenticated, "CERT_REVOKED") }
	if e.manager().Superseded(context.Background()) {
		t.Fatal("Superseded = true after a refused confirmation")
	}
}

// A renewal that has just replaced the pair: the stream that was refused had
// presented the replaced certificate, so one more Register is worth a try.
func TestSupersededAfterARenewalOfThisProcessReconnectsOnce(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.renewNow("certificate renewed")
	if !e.mgr.Superseded(context.Background()) {
		t.Fatal("Superseded = false right after a renewal")
	}
	if e.mgr.Superseded(context.Background()) {
		t.Fatal("Superseded = true twice for one renewal")
	}
}
