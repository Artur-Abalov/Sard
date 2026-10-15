// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew_test

import (
	"crypto/ecdsa"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
)

// docs/specs/agent/agent-certificate-renewal.feature: the rule on repeats and
// the state file (P19, P25, P28).

func temporary(text string) error { return &certrenew.CallError{Text: text} }

func final(text string) error { return &certrenew.CallError{Text: text, Final: true} }

func unsupported() error {
	return &certrenew.CallError{Text: "Unimplemented", Final: true, Unsupported: true}
}

func failWith(err error) func(int, *ecdsa.PublicKey) (string, error) {
	return func(int, *ecdsa.PublicKey) (string, error) { return "", err }
}

func (e *env) readState() certrenew.State {
	e.t.Helper()
	st, ok := certrenew.ReadState(os.ReadFile, e.statePath())
	if !ok {
		e.t.Fatalf("no state file")
	}
	return st
}

// Scenario: Неудачная попытка записывает файл состояния
func TestAFailedAttemptWritesTheStateFileAndWarns(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.server.renewFn = failWith(temporary("Unavailable INTERNAL_RETRYABLE"))
	e.renewNow("WARN certificate renewal failed")
	e.requireFirstFailureState()
	line := e.logs.waitFor(t, "WARN certificate renewal failed")
	if !strings.Contains(line, "last_error=Unavailable INTERNAL_RETRYABLE") || !strings.Contains(line, "next_attempt=2027-05-03T12:01:00Z") {
		t.Errorf("log = %q", line)
	}
}

func (e *env) requireFirstFailureState() {
	e.t.Helper()
	st := e.readState()
	if st.Serial != "1" || st.Failures != 1 || st.LastError != "Unavailable INTERNAL_RETRYABLE" || !st.LastAttempt.Equal(day(200)) || !st.NextAttempt.Equal(day(200).Add(time.Minute)) {
		e.t.Fatalf("state = %+v", st)
	}
	if info, _ := os.Stat(e.statePath()); info.Mode().Perm() != 0o600 {
		e.t.Errorf("mode = %v", info.Mode())
	}
}

// Scenario: Пауза растёт от минуты до часа со случайным разбросом
func TestThePauseGrowsFromAMinuteToAnHourWithJitter(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		rnd  float64
		want []time.Duration
	}{
		{0.999999, []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour}},
		{0, []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 30 * time.Minute, 30 * time.Minute, 30 * time.Minute}},
	} {
		e := newEnv(t)
		e.rnd = row.rnd
		e.server.renewFn = failWith(temporary("Unavailable INTERNAL_RETRYABLE"))
		e.start()
		e.clock.Advance(e.idle())
		for i, want := range row.want {
			d := e.idle()
			if diff := d - want; diff > time.Second || diff < -time.Second {
				t.Fatalf("rnd %v, pause %d = %v, want %v", row.rnd, i+1, d, want)
			}
			e.clock.Advance(d)
		}
		if st := e.readState(); st.Failures != len(row.want) {
			t.Errorf("failures = %d", st.Failures)
		}
	}
}

// Scenario: Отказ RESOURCE_EXHAUSTED выдерживает задержку из ответа
func TestTheDelayOfARetryInfoIsKept(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	refusal := &certrenew.CallError{Text: "ResourceExhausted RENEWAL_TOO_FREQUENT", RetryAfter: 40 * time.Minute}
	e.server.renewFn = failWith(refusal)
	e.start()
	e.clock.Advance(e.idle())
	if d := e.idle(); d < 40*time.Minute {
		t.Fatalf("pause = %v", d)
	}
	if got := e.readState().LastError; got != "ResourceExhausted RENEWAL_TOO_FREQUENT" {
		t.Errorf("last_error = %q", got)
	}
}

// Scenario: Старый сервер без продления не мешает работе агента
func TestAServerWithoutRenewalIsAskedOnceADay(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.server.renewFn = failWith(unsupported())
	e.start()
	e.clock.Advance(e.idle())
	for i := 0; i < 3; i++ {
		if d := e.idle(); d != 24*time.Hour {
			t.Fatalf("pause = %v", d)
		}
		e.clock.Advance(24 * time.Hour)
	}
	e.idle()
	if n := len(e.server.callsOf("renew")); n != 4 {
		t.Fatalf("RenewCertificate calls = %d", n)
	}
	if got := strings.Count(e.logs.all(), "the server does not support certificate renewal; retrying every 24h"); got != 1 {
		t.Errorf("the line was logged %d times:\n%s", got, e.logs.all())
	}
	e.requireUnsupportedState()
}

func (e *env) requireUnsupportedState() {
	e.t.Helper()
	st := e.readState()
	if !st.Unsupported || st.Failures != 0 || st.Serial != "1" || !st.LastAttempt.Equal(day(203)) || !st.NextAttempt.Equal(day(204)) {
		e.t.Errorf("state = %+v", st)
	}
}

// Scenario: Успешное продление сбрасывает счётчик неудач
func TestASuccessfulRenewalResetsTheFailureCount(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.rnd = 0.999999
	e.server.renewFn = func(n int, pub *ecdsa.PublicKey) (string, error) {
		if n == 6 {
			return e.server.issuePlain(pub)
		}
		return "", temporary("Unavailable INTERNAL_RETRYABLE")
	}
	e.start()
	e.clock.Advance(e.idle())
	for i := 0; i < 5; i++ {
		e.clock.Advance(e.idle())
	}
	e.logs.waitFor(t, "certificate renewed")
	if _, err := os.Stat(e.statePath()); err == nil {
		t.Fatal("the state file survived a successful renewal")
	}
	e.clock.Advance(e.idle()) // the threshold of the new certificate; the server fails again
	if d := e.idle(); d > time.Minute {
		t.Fatalf("pause after the first failure = %v", d)
	}
}

// Scenario: Последняя ошибка экранирует управляющие символы и не раскрывает ключи
func TestTheLastErrorIsOneLine(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.server.renewFn = failWith(final(`InvalidArgument BAD\nREASON`))
	e.renewNow("certificate renewal failed")
	if got := e.readState().LastError; got != `InvalidArgument BAD\nREASON` {
		t.Fatalf("last_error = %q", got)
	}
}
