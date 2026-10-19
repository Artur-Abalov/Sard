// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certtest"
)

// docs/specs/agent/agent-certificate-renewal.feature, П45 and П46: the lines
// tls.* of sard-agent check describe the pair that the start would select.

const (
	pairTenant = "11111111-1111-1111-1111-111111111111"
	pairAgent  = "22222222-2222-2222-2222-222222222222"
)

var pairT0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func pairDay(n int) time.Time { return pairT0.AddDate(0, 0, n) }

// pairHost is a healthy host whose certificate files are issued by one CA
// to one agent, and whose clock is set by the test.
type pairHost struct {
	*setupHost
	ca *certtest.CA
}

func newPairHost(t *testing.T, now time.Time) *pairHost {
	t.Helper()
	h := fullHost(t)
	h.clock.now = now
	ca := certtest.NewCA(t)
	h.write(h.cfg.TLS.CAFile, string(ca.PEM()), 0o644)
	return &pairHost{setupHost: h, ca: ca}
}

// install writes a pair of serial valid from..to; suffix is "" or ".new".
func (h *pairHost) install(serial int64, from, to time.Time, suffix string) {
	h.t.Helper()
	p := h.ca.NewPair(h.t, certtest.Spec{Tenant: pairTenant, Agent: pairAgent, Role: "source", Serial: serial, NotBefore: from, NotAfter: to})
	h.write(h.cfg.TLS.KeyFile+suffix, string(p.KeyPEM), 0o600)
	h.write(h.cfg.TLS.CertFile+suffix, string(p.CertPEM), 0o644)
}

// Scenario: check описывает новую пару которую выбрал бы старт
func TestCheckDescribesTheNewPairThatTheStartWouldSelect(t *testing.T) {
	h := newPairHost(t, pairDay(310))
	h.install(1, pairT0, pairDay(300), "")
	h.install(2, pairDay(200), pairDay(500), ".new")
	lines, _, _ := h.checkLines()
	id := line(t, lines, "tls.identity", "-")
	if id.status != "OK" || !strings.Contains(id.message, h.cfg.TLS.KeyFile+".new") || !strings.Contains(id.message, h.cfg.TLS.CertFile+".new") {
		t.Errorf("tls.identity %+v", id)
	}
	if exp := line(t, lines, "tls.expiry", "-"); exp.status != "OK" || !strings.Contains(exp.message, "valid until 2028-02-27") {
		t.Errorf("tls.expiry %+v", exp)
	}
	if chain := line(t, lines, "tls.chain", "-"); chain.status != "OK" || !strings.Contains(chain.message, h.cfg.TLS.CertFile+".new") {
		t.Errorf("tls.chain %+v", chain)
	}
}

// Scenario: check не убирает файлы которые убрал бы старт
func TestCheckKeepsTheFilesThatTheStartWouldRemove(t *testing.T) {
	h := newPairHost(t, pairDay(10))
	h.install(1, pairT0, pairDay(300), "")
	h.write(h.cfg.TLS.KeyFile+".new", "leftover", 0o600)
	lines, _, _ := h.checkLines()
	if _, err := os.Stat(h.cfg.TLS.KeyFile + ".new"); err != nil {
		t.Errorf("the key.new file is gone: %v", err)
	}
	if id := line(t, lines, "tls.identity", "-"); id.status != "OK" || !strings.Contains(id.message, h.cfg.TLS.CertFile) || strings.Contains(id.message, ".new") {
		t.Errorf("tls.identity %+v", id)
	}
	if exp := line(t, lines, "tls.expiry", "-"); exp.status != "OK" || !strings.Contains(exp.message, "2027-08-11") {
		t.Errorf("tls.expiry %+v", exp)
	}
}

// Scenario: check с истёкшими всеми парами называет истечение
func TestCheckNamesTheExpiryOfTheLatestIssuedExpiredPair(t *testing.T) {
	h := newPairHost(t, pairDay(600))
	h.install(1, pairT0, pairDay(300), "")
	h.install(2, pairDay(200), pairDay(500), ".new")
	lines, _, _ := h.checkLines()
	if exp := line(t, lines, "tls.expiry", "-"); exp.status != "ERROR" || exp.message != "the certificate expired on 2028-02-27" {
		t.Errorf("tls.expiry %+v", exp)
	}
}

// Scenario: check без согласованной пары сообщает о незавершённой регистрации как раньше
func TestCheckWithoutAConsistentPairReportsAnUnfinishedRegistration(t *testing.T) {
	h := newPairHost(t, pairDay(10))
	h.install(1, pairT0, pairDay(300), "")
	other := h.ca.NewPair(t, certtest.Spec{Tenant: pairTenant, Agent: pairAgent, Role: "source", Serial: 3, NotBefore: pairT0, NotAfter: pairDay(300)})
	h.write(h.cfg.TLS.KeyFile, string(other.KeyPEM), 0o600)
	lines, _, _ := h.checkLines()
	want := "tls.key_file is not the key of tls.cert_file: a registration was not finished"
	if id := line(t, lines, "tls.identity", "-"); id.status != "ERROR" || id.message != want {
		t.Errorf("tls.identity %+v", id)
	}
}
