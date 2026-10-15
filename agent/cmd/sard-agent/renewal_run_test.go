// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"testing"
	"time"
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
	h.server.waitCount(t, "heartbeat", 1)
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
	h.reconnect(2)
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
	h.reconnect(2)
	h.server.waitCount(t, "register C2", 1)
	h.requireEvents("register C1", "connect C1", "hello C1")
	if !contains(h.server.eventLog(), "confirm C2") || a.exited() {
		t.Fatalf("events = %v", h.server.eventLog())
	}
}

// reconnect lets the transport's pauses pass, a minute at a time, until the
// server has seen n Register calls.
func (h *renewalHost) reconnect(n int) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for h.server.count("register") < n {
		if time.Now().After(deadline) {
			h.t.Fatalf("the agent did not register %d times; events: %v", n, h.server.eventLog())
		}
		h.clock.Advance(time.Minute)
		time.Sleep(5 * time.Millisecond)
	}
}
