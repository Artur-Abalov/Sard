// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

//go:debug cryptocustomrand=1

package certrenew_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
	"github.com/Artur-Abalov/sard/agent/internal/certtest"
)

// withHexSerial replaces pair 1 by one whose serial has hex letters.
func (e *env) withHexSerial(serial int64) {
	e.t.Helper()
	e.p1 = e.ca.NewPair(e.t, certtest.Spec{Tenant: tenant, Agent: agent, Role: "source", Serial: serial, NotBefore: t0, NotAfter: day(300)})
	e.write("agent.key", e.p1.KeyPEM)
	e.write("agent.pem", e.p1.CertPEM)
}

// Scenario: Неудачная попытка записывает файл состояния
func TestTheStateNamesTheCertificateBeingRenewedByItsHexSerial(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.withHexSerial(0xabcdef12)
	e.server.renewFn = failWith(temporary("Unavailable INTERNAL_RETRYABLE"))
	e.renewNow("WARN certificate renewal failed")
	if st := e.readState(); st.Serial != "abcdef12" {
		t.Fatalf("state = %+v", st)
	}
}

func TestTheLogOfARenewalNamesTheOldAndTheNewSerialInHex(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.withHexSerial(0xabcdef12)
	e.server.serial = 0xab // the next certificate is 0xad
	e.renewNow("INFO certificate renewed")
	line := e.logs.waitFor(t, "INFO certificate renewed")
	if !strings.Contains(line, "old_serial=abcdef12") || !strings.Contains(line, "new_serial=ad ") {
		t.Fatalf("log = %q", line)
	}
}

func TestTheOldSerialOfAnAdoptedRenewalIsTheOneThatWasRenewed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.withHexSerial(0xabcdef12)
	e.server.confirm = func(n int) error {
		if n == 1 {
			return temporary("Unavailable INTERNAL_RETRYABLE")
		}
		return nil
	}
	e.renewNow("WARN certificate renewal failed")
	e.clock.Advance(e.idle())
	line := e.logs.waitFor(t, "INFO certificate renewed")
	if !strings.Contains(line, "old_serial=abcdef12") {
		t.Fatalf("log = %q", line)
	}
}

func TestWithoutARandSourceThePauseAfterAFailureIsJittered(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.server.renewFn = failWith(temporary("Unavailable INTERNAL_RETRYABLE"))
	e.startWith(certrenew.New(certrenew.Options{Files: e.files(), StatePath: e.statePath(), FS: e.fs, Caller: e.server, Clock: e.clock, Logger: slog.New(e.logs)}))
	e.mgr.Registered()
	e.idle()
	e.clock.Advance(200 * 24 * time.Hour)
	e.logs.waitFor(t, "WARN certificate renewal failed")
	if d := e.idle(); d < 30*time.Second || d > time.Minute {
		t.Fatalf("the first pause = %v, want within [30s, 1m]", d)
	}
}

// failingReader is an entropy source that is gone.
type failingReader struct{}

var errNoEntropy = errors.New("no entropy")

func (failingReader) Read([]byte) (int, error) { return 0, errNoEntropy }

func TestWithoutEntropyTheAttemptFailsBeforeAnyCall(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.startWith(certrenew.New(certrenew.Options{
		Files: e.files(), StatePath: e.statePath(), FS: e.fs, Caller: e.server, Clock: e.clock,
		Rand: func() float64 { return e.rnd }, Random: failingReader{}, Logger: slog.New(e.logs),
	}))
	e.mgr.Registered()
	e.idle()
	e.clock.Advance(200 * 24 * time.Hour)
	line := e.logs.waitFor(t, "WARN certificate renewal failed")
	if !strings.Contains(line, "generate the new key") || len(e.server.callsOf("renew")) != 0 {
		t.Fatalf("log = %q, calls = %d", line, len(e.server.callsOf("renew")))
	}
	if e.names() != "agent.key agent.pem ca.pem" {
		t.Fatalf("files = %s", e.names())
	}
}

// refusingFS fails the removal and the write of the state file.
type refusingFS struct {
	certpair.OSFS
	removeFails, writeFails bool
}

func (f refusingFS) Remove(name string) error {
	if f.removeFails && filepath.Base(name) == certrenew.StateFile {
		return os.ErrPermission
	}
	return f.OSFS.Remove(name)
}

func (f refusingFS) WriteFile(path string, data []byte, mode os.FileMode) error {
	if f.writeFails && filepath.Base(path) == certrenew.StateFile {
		return os.ErrPermission
	}
	return f.OSFS.WriteFile(path, data, mode)
}

func TestAStateFileThatCannotBeWrittenIsLoggedAndTheRenewalGoesOn(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fs = refusingFS{writeFails: true}
	e.server.renewFn = failWith(temporary("Unavailable INTERNAL_RETRYABLE"))
	e.renewNow("WARN certificate renewal failed")
	line := e.logs.waitFor(t, "writing the certificate renewal state failed")
	if !strings.Contains(line, "error="+os.ErrPermission.Error()) {
		t.Fatalf("log = %q", line)
	}
	d := e.idle()
	e.clock.Advance(d)
	e.logs.waitFor(t, "failures=2")
}

func TestAStateFileThatCannotBeRemovedIsLoggedAndTheRenewalIsDone(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.fs = refusingFS{removeFails: true}
	e.renewNow("INFO certificate renewed")
	line := e.logs.waitFor(t, "removing the certificate renewal state failed")
	if !strings.Contains(line, "error="+os.ErrPermission.Error()) {
		t.Fatalf("log = %q", line)
	}
}

// Scenario: На двух третях срока агент запрашивает сертификат на новом ключе
func TestExactlyAtTheThresholdTheAgentRenewsWithoutWaiting(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.clock.Advance(200 * 24 * time.Hour)
	e.start()
	if got := e.server.next(); got != "renew" {
		t.Fatalf("first call = %s", got)
	}
}

func TestOneNanosecondBeforeTheThresholdTheAgentWaitsForIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.clock.Advance(200*24*time.Hour - time.Nanosecond)
	e.start()
	if d := e.idle(); d != time.Nanosecond {
		t.Fatalf("wait = %v", d)
	}
	if n := len(e.server.callsOf("renew")); n != 0 {
		t.Fatalf("RenewCertificate calls = %d before the threshold", n)
	}
	e.clock.Advance(time.Nanosecond)
	if got := e.server.next(); got != "renew" {
		t.Fatalf("first call = %s", got)
	}
}

func TestAStateThatCannotBeEncodedIsAnError(t *testing.T) {
	t.Parallel()
	st := certrenew.State{Serial: "a", LastAttempt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := certrenew.WriteState(certpair.OSFS{}, filepath.Join(t.TempDir(), "s.json"), st); err == nil {
		t.Fatal("a state with a year beyond 9999 was written")
	}
}

func TestRemoveStateReportsAFileItCannotRemove(t *testing.T) {
	t.Parallel()
	err := certrenew.RemoveState(refusingFS{removeFails: true}, filepath.Join(t.TempDir(), certrenew.StateFile))
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v", err)
	}
}

func TestAStateReadWithAnErrorIsAsIfThereWasNone(t *testing.T) {
	t.Parallel()
	read := func(string) ([]byte, error) { return []byte(`{"serial":"a"}`), os.ErrPermission }
	if st, ok := certrenew.ReadState(read, "x"); ok {
		t.Fatalf("state = %+v", st)
	}
}

// Scenario: Пауза растёт от минуты до часа со случайным разбросом
func TestADelayAfterNoFailureAtAllIsTheLongestOne(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, -1, -100} {
		got := certrenew.Delay(n, func() float64 { return 0.999999 })
		if diff := got - time.Hour; diff > time.Second || diff < -time.Second {
			t.Errorf("Delay(%d) = %v, want an hour", n, got)
		}
	}
}

// Scenario: Сервер недоступен при подтверждении на старте, и агент не регистрируется старой парой
func TestWithoutReconnectDelaysAdoptWaitsAMinuteBetweenTries(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.withNewPair()
	e.server.confirm = func(n int) error {
		if n == 1 {
			return temporary("Unavailable")
		}
		return nil
	}
	mgr := certrenew.New(certrenew.Options{Files: e.files(), StatePath: e.statePath(), FS: e.fs, Caller: e.server, Clock: e.clock, Logger: slog.New(e.logs)})
	done := make(chan error, 1)
	go func() { done <- mgr.Adopt(context.Background(), e.selection()) }()
	if d := e.idle(); d != time.Minute {
		t.Fatalf("pause = %v", d)
	}
	e.clock.Advance(time.Minute)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAdoptLogsEachFailedConfirmation(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.withNewPair()
	e.server.confirm = func(n int) error {
		if n == 1 {
			return temporary("Unavailable INTERNAL_RETRYABLE")
		}
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- e.manager().Adopt(context.Background(), e.selection()) }()
	e.clock.Advance(e.idle())
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if line := e.logs.waitFor(t, "WARN confirming the renewed certificate failed"); !strings.Contains(line, "error=Unavailable INTERNAL_RETRYABLE") {
		t.Fatalf("log = %q", line)
	}
}

func TestAdoptLogsWhatItRemovedWhenTheServerRefusesForGood(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.withNewPair()
	e.server.confirm = func(int) error { return final("Unauthenticated CERT_REVOKED") }
	if err := e.manager().Adopt(context.Background(), e.selection()); !errors.Is(err, certrenew.ErrDiscarded) {
		t.Fatal(err)
	}
	line := e.logs.waitFor(t, "WARN the server refused the renewed certificate; removed it")
	for _, want := range []string{"agent.key.new", "agent.pem.new", "error=Unauthenticated CERT_REVOKED"} {
		if !strings.Contains(line, want) {
			t.Errorf("log = %q, want %q in it", line, want)
		}
	}
}
