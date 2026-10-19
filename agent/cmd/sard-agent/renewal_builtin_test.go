// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
	"time"
)

// docs/specs/agent/agent-certificate-renewal.feature, tag @fake: the built-in agent.

const builtInPhrase = "revoke the built-in agent in the console; it enrolls again by itself"

// Scenario: Встроенный агент с истёкшим сертификатом не завершается
func TestTheBuiltInAgentWithAnExpiredCertificateDoesNotExit(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.clock.Set(renewDay(300))
	a := h.run("--enroll-token-file", h.builtIn("spent-token"))
	a.waitStderr(t, "CERT_EXPIRED", 1)
	h.clock.Advance(time.Hour)
	a.waitStderr(t, "CERT_EXPIRED", 2)
	h.clock.Advance(time.Hour)
	a.waitStderr(t, "CERT_EXPIRED", 3)
	h.clock.Advance(30 * time.Minute)
	if a.exited() || a.stderrLines("CERT_EXPIRED") != 3 || len(h.server.eventLog()) != 0 {
		t.Fatalf("exited = %v, stderr = %q, events = %v", a.exited(), a.stderr.String(), h.server.eventLog())
	}
	for _, line := range strings.Split(strings.TrimSpace(a.stderr.String()), "\n") {
		if !strings.Contains(line, builtInPhrase) || strings.Contains(line, "sudo sard-agent enroll") || strings.Contains(line, refusalSuffix) {
			t.Errorf("line = %q", line)
		}
	}
}

// Scenario: Встроенный агент после окончательного отказа Register не завершается и не повторяет вызов
func TestTheBuiltInAgentAfterAFinalRefusalDoesNotExitOrCallAgain(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.server.register = refuseRegister("AGENT_REVOKED")
	a := h.run("--enroll-token-file", h.builtIn("spent-token"))
	a.waitStderr(t, "AGENT_REVOKED", 1)
	h.clock.Advance(time.Hour)
	a.waitStderr(t, "AGENT_REVOKED", 2)
	h.clock.Advance(time.Hour)
	a.waitStderr(t, "AGENT_REVOKED", 3)
	if a.exited() || h.server.count("register") != 1 {
		t.Fatalf("exited = %v, events = %v", a.exited(), h.server.eventLog())
	}
}

// Scenario: Новый токен в файле выводит встроенный агент из отказа
func TestANewTokenTakesTheBuiltInAgentOutOfTheRefusal(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.answerEnroll()
	h.server.register = func(serial string, _ int) error {
		if serial == "1" {
			return refuseRegister("AGENT_REVOKED")(serial, 0)
		}
		return nil
	}
	tokenFile := h.builtIn("spent-token")
	a := h.run("--enroll-token-file", tokenFile)
	a.waitStderr(t, "AGENT_REVOKED", 1)
	if err := writeTokenText(tokenFile, h.newToken()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for h.server.count("enroll") < 1 {
		if time.Now().After(deadline) || a.exited() {
			t.Fatalf("no Enroll; exited = %v, stderr = %q", a.exited(), a.stderr.String())
		}
		h.clock.Advance(selfEnrollPollInterval)
		time.Sleep(5 * time.Millisecond)
	}
	h.server.waitCount(t, "register C4d", 1)
	if a.exited() {
		t.Fatalf("the agent exited: %q", a.stderr.String())
	}
}

// Scenario: SIGTERM в состоянии отказа завершает встроенного агента с кодом 0
func TestSIGTERMInTheRefusedStateEndsTheBuiltInAgentWithCode0(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.server.register = refuseRegister("AGENT_REVOKED")
	a := h.run("--enroll-token-file", h.builtIn("spent-token"))
	a.waitStderr(t, "AGENT_REVOKED", 1)
	if code := a.stop(t); code != 0 {
		t.Fatalf("code = %d", code)
	}
}

// Scenario: Встроенный агент продлевает сертификат так же
func TestTheBuiltInAgentRenewsItsCertificateToo(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	marker := h.builtIn("spent-token")
	_ = marker
	before := h.read("agent.pem.token-sha256")
	h.run("--enroll-token-file", marker)
	h.server.waitCount(t, "hello", 1)
	h.renewalPause()
	h.clock.Advance(200 * 24 * time.Hour)
	h.waitLog("certificate renewed")
	if h.server.count("enroll") != 0 || h.read("agent.pem.token-sha256") != before || h.exists("agent.pem.new") {
		t.Fatalf("events = %v, files = %s", h.server.eventLog(), h.names())
	}
}
