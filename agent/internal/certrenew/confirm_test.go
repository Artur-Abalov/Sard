// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew_test

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// docs/specs/agent/agent-certificate-renewal.feature: the rule on failures in the
// middle of a renewal (P18, P20).

// Scenario: Каталог tls недоступен на запись — пара не меняется, ошибка в файле состояния
func TestAnUnwritableDirectoryLeavesThePairAndIsNamedInTheState(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fs = failingFS{failWrite: "agent.key.new", dir: e.dir}
	e.renewNow("certificate renewal failed")
	if e.names() != "agent.key agent.pem ca.pem" || e.read("agent.key") != string(e.p1.KeyPEM) {
		t.Fatalf("files = %s", e.names())
	}
	if n := len(e.server.callsOf("confirm")); n != 0 {
		t.Errorf("%d confirmations", n)
	}
	if got := e.readState().LastError; !strings.Contains(got, e.dir) {
		t.Errorf("last_error = %q does not name %s", got, e.dir)
	}
}

// Scenario: Сбой записи сертификата после записи ключа не оставляет половины пары
func TestAFailedCertificateWriteLeavesNoHalfAPair(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fs = failingFS{failWrite: "agent.pem.new", dir: e.dir}
	e.renewNow("certificate renewal failed")
	if e.names() != "agent.key agent.pem ca.pem" {
		t.Fatalf("files = %s", e.names())
	}
	if n := len(e.server.callsOf("confirm")); n != 0 {
		t.Errorf("%d confirmations", n)
	}
}

// Scenario: Окончательный отказ в подтверждении удаляет новую пару и оставляет текущую
func TestAFinalRefusalOfTheConfirmationRemovesTheNewPair(t *testing.T) {
	t.Parallel()
	for _, refusal := range []error{
		reasoned(codes.Unauthenticated, "CERT_SUPERSEDED"),
		status.Error(codes.InvalidArgument, "x"),
		status.Error(codes.FailedPrecondition, "x"),
		status.Error(codes.PermissionDenied, "x"),
		status.Error(codes.Unimplemented, "x"),
	} {
		e := newEnv(t)
		e.server.confirm = func(int) error { return refusal }
		e.renewNow("certificate renewal failed")
		if e.names() != "agent.key agent.pem ca.pem" || e.read("agent.key") != string(e.p1.KeyPEM) {
			t.Fatalf("%v: files = %s", refusal, e.names())
		}
		if d := e.idle(); d < 30*time.Second || d > time.Minute {
			t.Errorf("%v: next attempt in %v, want the first pause", refusal, d)
		}
		e.clock.Advance(time.Minute)
		e.logs.waitFor(t, "failures=2")
		if n := len(e.server.callsOf("renew")); n != 2 {
			t.Errorf("%v: RenewCertificate calls = %d, want a new request", refusal, n)
		}
	}
}

// Scenario: Временный сбой подтверждения повторяет только подтверждение
func TestATemporaryRefusalOfTheConfirmationRepeatsOnlyTheConfirmation(t *testing.T) {
	t.Parallel()
	for _, temporary := range []error{
		reasoned(codes.Unavailable, "INTERNAL_RETRYABLE"),
		status.Error(codes.Internal, "x"),
		status.Error(codes.DeadlineExceeded, "x"),
	} {
		e := newEnv(t)
		e.server.confirm = func(n int) error {
			if n == 1 {
				return temporary
			}
			return nil
		}
		e.renewNow("certificate renewal failed")
		if !e.exists("agent.key.new") || !e.exists("agent.pem.new") {
			t.Fatalf("%v: the new pair was removed: %s", temporary, e.names())
		}
		e.clock.Advance(e.idle())
		e.logs.waitFor(t, "certificate renewed")
		confirms := e.server.callsOf("confirm")
		if n := len(e.server.callsOf("renew")); n != 1 || len(confirms) != 2 || confirms[0].presented != "2" || confirms[1].presented != "2" {
			t.Fatalf("%v: renewals = %d, confirmations = %+v", temporary, n, confirms)
		}
		if e.names() != "agent.key agent.pem ca.pem" || e.read("agent.pem") != e.server.issued[0] {
			t.Errorf("%v: files = %s", temporary, e.names())
		}
	}
}

// Scenario: Журнал и вывод продления не содержат ключей, CSR и сертификатов
func TestTheLogHoldsNoKeyMaterial(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.renewNow("certificate renewed")
	log := e.logs.all()
	for _, secret := range []string{"PRIVATE KEY", "CERTIFICATE", "BEGIN"} {
		if strings.Contains(log, secret) {
			t.Errorf("the log contains %q:\n%s", secret, log)
		}
	}
	for _, pemText := range []string{e.read("agent.key"), e.read("agent.pem")} {
		if body := strings.Split(pemText, "\n")[1]; strings.Contains(log, body) {
			t.Errorf("the log holds the body of a key or certificate")
		}
	}
}
