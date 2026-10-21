// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"crypto/ecdsa"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// docs/specs/agent/agent-certificate-renewal.feature, tag @renewal: the agent whole.

// registered runs the agent until it holds a stream.
func (h *renewalHost) registered() *runningAgent {
	h.t.Helper()
	a := h.run()
	h.server.waitCount(h.t, "hello", 1)
	return a
}

// Scenario: Продление не переоткрывает поток команд
func TestRenewalDoesNotReopenTheStream(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	a := h.registered()
	if d := h.renewalPause(); d != 200*24*time.Hour {
		t.Fatalf("the first wait = %v", d)
	}
	h.clock.Advance(200 * 24 * time.Hour)
	h.waitLog("certificate renewed")
	if h.server.count("register") != 1 || h.server.count("connect") != 1 || a.exited() {
		t.Fatalf("events = %v", h.server.eventLog())
	}
	h.heartbeat()
}

// heartbeat lets the stream's heartbeat timer come due and waits for the beat;
// the timer may have been started after the renewal's clock jump.
func (h *renewalHost) heartbeat() {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.server.count("heartbeat") < 1 {
		if time.Now().After(deadline) {
			h.t.Fatalf("no heartbeat; events: %v", h.server.eventLog())
		}
		h.clock.Advance(30 * time.Second)
		time.Sleep(5 * time.Millisecond)
	}
}

// Scenario: Подтверждение идёт новой парой по отдельному соединению
func TestTheConfirmationComesByAConnectionOfItsOwn(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.registered()
	h.renewalPause()
	h.clock.Advance(200 * 24 * time.Hour)
	h.waitLog("certificate renewed")
	events := h.server.eventLog()
	used := map[string]string{}
	for i, e := range events {
		used[e] = h.server.connectionOf(i)
	}
	if used["confirm C2"] == "" || used["confirm C2"] == used["renew C1"] || used["confirm C2"] == used["register C1"] || used["renew C1"] == used["register C1"] {
		t.Fatalf("events = %v, connections = %v", events, used)
	}
}

// Scenario: Следующее подключение идёт с новым сертификатом
func TestTheNextConnectionPresentsTheNewCertificate(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.registered()
	h.renewalPause()
	h.clock.Advance(200 * 24 * time.Hour)
	h.waitLog("certificate renewed")
	h.server.dropStreams()
	h.reconnect()
	if got := h.server.eventLog(); !contains(got, "register C2") {
		t.Fatalf("events = %v", got)
	}
}

// Scenario: Отказ CERT_SUPERSEDED при годной новой паре переводит агента на неё
func TestASupersededRegisterWithAUsableNewPairMovesTheAgentToIt(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	a := h.registered()
	h.install(h.issuePair(2, renewDay(1), renewDay(500)), ".new")
	h.clock.Set(renewDay(2))
	h.server.register = func(serial string, _ int) error {
		if serial == "1" {
			return refuseRegister("CERT_SUPERSEDED")(serial, 0)
		}
		return nil
	}
	h.server.dropStreams()
	h.reconnect()
	h.server.waitCount(t, "register C2", 1)
	h.requireEvents("register C1", "connect C1", "hello C1")
	if !contains(h.server.eventLog(), "confirm C2") || a.exited() {
		t.Fatalf("events = %v", h.server.eventLog())
	}
}

// reconnect lets the transport's pauses pass, a minute at a time, until the
// server has seen a second Register call.
func (h *renewalHost) reconnect() {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.server.count("register") < 2 {
		if time.Now().After(deadline) {
			h.t.Fatalf("the agent did not register twice; events: %v", h.server.eventLog())
		}
		h.clock.Advance(time.Minute)
		time.Sleep(5 * time.Millisecond)
	}
}

// Scenario: Неудачное продление не останавливает агента и не трогает поток
func TestAFailingRenewalDoesNotStopTheAgentOrTouchTheStream(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.server.renew = func(string, int, *ecdsa.PublicKey) (string, error) { return "", status.Error(codes.Internal, "boom") }
	a := h.registered()
	h.renewalPause()
	h.clock.Advance(200 * 24 * time.Hour)
	for n := 1; n <= 3; n++ {
		h.advanceUntilCount("renew", n)
		h.waitLog("failures=" + strconv.Itoa(n))
	}
	if a.exited() || h.server.count("connect") != 1 || h.server.count("register") != 1 {
		t.Fatalf("exited = %v, events = %v", a.exited(), h.server.eventLog())
	}
}

// advanceUntilCount moves the clock an hour at a time until the server has seen n events with the prefix.
func (h *renewalHost) advanceUntilCount(prefix string, n int) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.server.count(prefix) < n {
		if time.Now().After(deadline) {
			h.t.Fatalf("the server saw %d x %q, want %d; events: %v", h.server.count(prefix), prefix, n, h.server.eventLog())
		}
		h.clock.Advance(time.Hour)
		time.Sleep(5 * time.Millisecond)
	}
}
