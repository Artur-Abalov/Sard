// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/certtest"
)

// docs/specs/agent/agent-certificate-renewal.feature, tags @start and @renewal.

// Scenario: Перезапуск после записи новой пары до подтверждения
func TestARestartAfterTheNewPairWasWrittenConfirmsItBeforeRegistering(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	p2 := h.issuePair(2, renewDay(200), renewDay(500))
	h.install(p2, ".new")
	h.clock.Set(renewDay(201))
	h.run()
	h.server.waitCount(t, "register", 1)
	if ev := h.server.eventLog(); len(ev) < 2 || ev[0] != "confirm C2" || ev[1] != "register C2" {
		t.Fatalf("events = %v", ev)
	}
	if h.names() != "agent.key agent.pem ca.pem" || h.read("agent.pem") != string(p2.CertPEM) || h.read("agent.key") != string(p2.KeyPEM) {
		t.Fatalf("files = %s", h.names())
	}
}

// registerIs makes the server refuse Register as the test says.
func refuseRegister(reason string) func(string, int) error {
	return func(string, int) error {
		return refusedWith(codes.Unauthenticated, "refused", "sard.dev", reason, nil)(nil)
	}
}

// requireEvents checks the first events the server saw.
func (h *renewalHost) requireEvents(want ...string) {
	h.t.Helper()
	got := h.server.eventLog()
	if len(got) < len(want) {
		h.t.Fatalf("events = %v, want to start with %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			h.t.Fatalf("events = %v, want to start with %v", got, want)
		}
	}
}

// requireNoEventWith checks that no call was made with the certificate.
func (h *renewalHost) requireNoEventWith(serial string) {
	h.t.Helper()
	for _, e := range h.server.eventLog() {
		if strings.HasSuffix(e, " C"+serial) {
			h.t.Fatalf("events = %v: a call was made with C%s", h.server.eventLog(), serial)
		}
	}
}

// Scenario: Перезапуск после подтверждения до переименования
func TestARestartAfterTheConfirmationButBeforeTheRenameNeverUsesTheOldCertificate(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	p2 := h.issuePair(2, renewDay(200), renewDay(500))
	h.install(p2, ".new")
	h.clock.Set(renewDay(201))
	h.server.register = func(serial string, _ int) error {
		if serial == "1" {
			return refuseRegister("CERT_SUPERSEDED")(serial, 0)
		}
		return nil
	}
	h.run()
	h.server.waitCount(t, "register", 1)
	h.requireEvents("confirm C2", "register C2")
	h.requireNoEventWith("1")
	if h.names() != "agent.key agent.pem ca.pem" || h.read("agent.pem") != string(p2.CertPEM) {
		t.Fatalf("files = %s", h.names())
	}
}

// Scenario: Перезапуск между двумя переименованиями
func TestARestartBetweenTheTwoRenamesConfirmsTheNewCertificate(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	p2 := h.issuePair(2, renewDay(200), renewDay(500))
	h.writeFile("agent.key", p2.KeyPEM, 0o600)
	h.writeFile("agent.pem.new", p2.CertPEM, 0o644)
	h.clock.Set(renewDay(201))
	h.run()
	h.server.waitCount(t, "register", 1)
	h.requireEvents("confirm C2", "register C2")
	if h.names() != "agent.key agent.pem ca.pem" || h.read("agent.pem") != string(p2.CertPEM) {
		t.Fatalf("files = %s", h.names())
	}
}

// Scenario: Недописанная новая пара удаляется, агент работает с текущей
func TestAnIncompleteNewPairIsRemovedAndTheAgentRegistersWithTheCurrentOne(t *testing.T) {
	t.Parallel()
	rows := map[string]func(h *renewalHost, p2 certtest.Pair){
		"only key": func(h *renewalHost, p2 certtest.Pair) { h.writeFile("agent.key.new", p2.KeyPEM, 0o600) },
		"mismatch": func(h *renewalHost, p2 certtest.Pair) {
			h.writeFile("agent.key.new", p2.KeyPEM, 0o600)
			h.writeFile("agent.pem.new", h.issuePair(3, renewDay(5), renewDay(9)).CertPEM, 0o644)
		},
		"empty": func(h *renewalHost, p2 certtest.Pair) {
			h.writeFile("agent.key.new", p2.KeyPEM, 0o600)
			h.writeFile("agent.pem.new", nil, 0o644)
		},
		"temp only": func(h *renewalHost, _ certtest.Pair) { h.writeFile(certpair.TempPrefix+"42", []byte("x"), 0o600) },
	}
	for name, setup := range rows {
		h := newRenewalHost(t)
		setup(h, h.issuePair(2, renewDay(2), renewDay(9)))
		h.clock.Set(renewDay(3))
		h.run()
		h.server.waitCount(t, "register", 1)
		h.requireEvents("register C1")
		if h.names() != "agent.key agent.pem ca.pem" || h.server.count("confirm") != 0 {
			t.Fatalf("%s: files = %s", name, h.names())
		}
		if log := h.log.String(); !strings.Contains(log, h.path("")) {
			t.Errorf("%s: the log names no removed path:\n%s", name, log)
		}
	}
}

// Scenario: Сервер недоступен при подтверждении на старте, и агент не регистрируется старой парой
func TestAnUnreachableServerAtConfirmationKeepsTheAgentFromRegisteringWithTheOldPair(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.rnd = 1
	p2 := h.issuePair(2, renewDay(200), renewDay(500))
	h.install(p2, ".new")
	h.clock.Set(renewDay(201))
	h.server.confirm = func(_ string, n int) error {
		if n <= 3 {
			return status.Error(codes.Unavailable, "down")
		}
		return nil
	}
	h.run()
	for _, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		if d := h.clock.WaitTimer(t); d != want {
			t.Fatalf("pause = %v, want %v", d, want)
		}
		h.clock.Advance(want)
	}
	h.server.waitCount(t, "register", 1)
	h.requireNoEventWith("1")
	if h.server.count("confirm") != 4 || h.server.eventLog()[4] != "register C2" {
		t.Fatalf("events = %v", h.server.eventLog())
	}
}

// Scenario: Окончательный отказ в подтверждении на старте возвращает агента к текущей паре
func TestAFinalRefusalAtConfirmationReturnsTheAgentToTheCurrentPair(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	// Before the threshold of pair 1 (day 200): no renewal runs beside the check of the files.
	h.install(h.issuePair(2, renewDay(100), renewDay(400)), ".new")
	h.clock.Set(renewDay(101))
	h.server.confirm = func(string, int) error {
		return refusedWith(codes.Unauthenticated, "no", "sard.dev", "CERT_REVOKED", nil)(nil)
	}
	h.run()
	h.server.waitCount(t, "register", 1)
	h.requireEvents("confirm C2", "register C1")
	if h.names() != "agent.key agent.pem ca.pem" {
		t.Fatalf("files = %s", h.names())
	}
}

// Scenario: Выбирается годная пара с самым поздним NotBefore
func TestTheAgentRegistersWithTheLatestIssuedUsablePair(t *testing.T) {
	t.Parallel()
	other := "33333333-3333-3333-3333-333333333333"
	rows := []struct {
		name       string
		main, next func(h *renewalHost) certtest.Pair
		now        time.Time
		presented  string
		keepsNew   bool
	}{
		{"expired new", nil, func(h *renewalHost) certtest.Pair { return h.issuePair(2, renewDay(10), renewDay(20)) }, renewDay(30), "1", false},
		{"another agent", nil, func(h *renewalHost) certtest.Pair {
			return h.ca.NewPair(h.t, certtest.Spec{Tenant: testTenant, Agent: other, Role: "source", Serial: 2, NotBefore: renewDay(100), NotAfter: renewDay(400)})
		}, renewDay(120), "1", false},
		{"earlier new", func(h *renewalHost) certtest.Pair { return h.issuePair(1, renewDay(10), renewDay(300)) }, func(h *renewalHost) certtest.Pair { return h.issuePair(2, renewDay(5), renewDay(300)) }, renewDay(20), "1", true},
		{"expired main", func(h *renewalHost) certtest.Pair { return h.issuePair(1, renewT0, renewDay(10)) }, func(h *renewalHost) certtest.Pair { return h.issuePair(2, renewDay(5), renewDay(500)) }, renewDay(12), "2", false},
	}
	for _, row := range rows {
		h := newRenewalHost(t)
		if row.main != nil {
			h.install(row.main(h), "")
		}
		h.install(row.next(h), ".new")
		h.clock.Set(row.now)
		h.run()
		h.server.waitCount(t, "register", 1)
		if got := h.server.eventLog(); got[len(got)-1] != "register C"+row.presented {
			t.Fatalf("%s: events = %v", row.name, got)
		}
		if h.exists("agent.key.new") != row.keepsNew {
			t.Fatalf("%s: files = %s", row.name, h.names())
		}
	}
}

// Scenario: После уменьшения срока выбирается новая пара, хотя она истекает раньше старой
func TestAfterAShorterLifetimeTheNewPairIsChosenThoughItExpiresFirst(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	p2b := h.issuePair(2, renewDay(200), renewDay(230))
	h.install(p2b, ".new")
	h.clock.Set(renewDay(201))
	h.server.register = func(serial string, _ int) error {
		if serial == "1" {
			return refuseRegister("CERT_SUPERSEDED")(serial, 0)
		}
		return nil
	}
	h.run()
	h.server.waitCount(t, "register", 1)
	h.requireEvents("confirm C2", "register C2")
	h.requireNoEventWith("1")
	if h.read("agent.pem") != string(p2b.CertPEM) || h.exists("agent.pem.new") {
		t.Fatalf("files = %s", h.names())
	}
}

// Scenario: Без единой согласованной пары агент сообщает о незавершённой регистрации
func TestWithoutAConsistentPairTheAgentReportsAnIncompleteEnrollment(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.writeFile("agent.key", h.issuePair(3, renewT0, renewDay(300)).KeyPEM, 0o600)
	a := h.run()
	if code := a.wait(t); code != 1 || !strings.Contains(a.stderr.String(), incompleteEnrollment) || len(h.server.eventLog()) != 0 {
		t.Fatalf("code = %d, stderr = %q, events = %v", code, a.stderr.String(), h.server.eventLog())
	}
}

// Scenario: Отказ CERT_SUPERSEDED без другой пары завершает агента с командой перерегистрации
func TestASupersededCertificateWithoutASpareEndsTheAgentWithTheReenrollCommand(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.server.register = refuseRegister("CERT_SUPERSEDED")
	a := h.run()
	h.requireRefusalLine(a, "CERT_SUPERSEDED")
}

// Scenario: Отказ сервера CERT_EXPIRED называет команду перерегистрации
func TestAServerRefusalForAnExpiredCertificateNamesTheReenrollCommand(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.server.register = refuseRegister("CERT_EXPIRED")
	a := h.run()
	h.requireRefusalLine(a, "reason CERT_EXPIRED")
}

// reenroll is the command of П22 for this host.
func (h *renewalHost) reenroll() string {
	return "sudo sard-agent enroll --force --config " + h.cfg + " --server " + h.address + " --token <TOKEN>"
}

// requireRefusalLine checks code 78 and one line with the reason, the command and the suffix.
func (h *renewalHost) requireRefusalLine(a *runningAgent, wants ...string) {
	h.t.Helper()
	code := a.wait(h.t)
	line := strings.TrimSuffix(a.stderr.String(), "\n")
	if code != 78 || strings.Contains(line, "\n") || !strings.HasPrefix(line, "sard-agent: ") || !strings.HasSuffix(line, refusalSuffix) || !strings.Contains(line, h.reenroll()) {
		h.t.Fatalf("code = %d, stderr = %q", code, a.stderr.String())
	}
	requireContains(h.t, line, wants, true)
}

// Scenario: Истёкший сертификат останавливает агента до подключения с кодом 78
func TestAnExpiredCertificateStopsTheAgentBeforeConnectingWithCode78(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.clock.Set(renewDay(300))
	a := h.run()
	h.requireRefusalLine(a, "CERT_EXPIRED", "tls.cert_file", h.path("agent.pem"), "expired at 2027-08-11T12:00:00Z")
	if strings.Contains(a.stdout.String(), "connecting to") || len(h.server.eventLog()) != 0 {
		t.Fatalf("stdout = %q, events = %v", a.stdout.String(), h.server.eventLog())
	}
	if strings.Contains(a.stderr.String(), "PRIVATE KEY") {
		t.Fatal("key material in stderr")
	}
}

// Scenario: Граница истечения проверяется по часам агента
func TestTheExpiryIsCheckedByTheAgentsClockAtTheSecond(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.clock.Set(renewDay(300).Add(-time.Second))
	h.run()
	h.server.waitCount(t, "register", 1)
}

// Scenario: Истёкший основной сертификат при годной новой паре не останавливает агента
func TestAnExpiredCurrentCertificateWithAUsableNewPairDoesNotStopTheAgent(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.install(h.issuePair(2, renewDay(250), renewDay(550)), ".new")
	h.clock.Set(renewDay(301))
	h.run()
	h.server.waitCount(t, "register", 1)
	h.requireEvents("confirm C2", "register C2")
}

// Scenario: Агент без флага файла токена с тем же отказом выходит с кодом 78
func TestWithoutTheTokenFileFlagAFinalRefusalEndsTheAgentWithCode78(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.server.register = refuseRegister("AGENT_REVOKED")
	a := h.run()
	if code := a.wait(t); code != 78 {
		t.Fatalf("code = %d", code)
	}
}

// Scenario: Юнит службы разрешает агенту запись в каталог tls
func TestTheUnitLetsTheAgentWriteToTheTLSDirectory(t *testing.T) {
	t.Parallel()
	if got := serviceSetting(t, "ReadWritePaths"); got != "-/etc/sard/tls" {
		t.Errorf("ReadWritePaths = %q", got)
	}
	if got := serviceSetting(t, "ProtectSystem"); got != "strict" {
		t.Errorf("ProtectSystem = %q", got)
	}
}
