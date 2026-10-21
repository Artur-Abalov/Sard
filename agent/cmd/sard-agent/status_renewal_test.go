// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
)

// docs/specs/agent/agent-certificate-renewal.feature, tag @local: sard-agent status (П26).

// statusAt runs "sard-agent status" with the clock at now.
func (h *renewalHost) statusAt(now time.Time) (code int, stdout, stderr string) {
	return h.statusWith(now, os.ReadFile)
}

func (h *renewalHost) statusWith(now time.Time, read func(string) ([]byte, error)) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	deps := statusDeps{readFile: read, stat: os.Stat, now: func() time.Time { return now }}
	code = runStatus([]string{"--config", h.cfg}, &out, &errOut, deps)
	return code, out.String(), errOut.String()
}

func (h *renewalHost) writeState(serial string) {
	h.t.Helper()
	st := certrenew.State{Serial: serial, Failures: 3, LastError: "Unavailable INTERNAL_RETRYABLE", LastAttempt: renewDay(210), NextAttempt: time.Date(2027, 5, 13, 12, 4, 0, 0, time.UTC)}
	if err := os.MkdirAll(h.stateDir, 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := certrenew.WriteState(certpair.OSFS{}, filepath.Join(h.stateDir, certrenew.StateFile), st); err != nil {
		h.t.Fatal(err)
	}
}

const statusHead = "agent_id: " + testAgent + "\ntenant_id: " + testTenant + "\nrole: source\ncertificate_not_after: 2027-08-11T12:00:00Z\n"

// Scenario: status называет состояние сертификата
func TestStatusNamesTheStateOfTheCertificate(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name  string
		now   time.Time
		state func(h *renewalHost)
		tail  func(h *renewalHost) string
	}{
		{"ok", renewDay(10), nil, func(*renewalHost) string { return "certificate_days_left: 290\ncertificate_state: ok\n" }},
		{"due", renewDay(200), nil, func(*renewalHost) string { return "certificate_days_left: 100\ncertificate_state: renewal-due\n" }},
		{"failing", renewDay(210), func(h *renewalHost) { h.writeState("1") }, func(*renewalHost) string {
			return "certificate_days_left: 90\ncertificate_state: renewal-failing\nrenewal_failures: 3\nrenewal_last_error: Unavailable INTERNAL_RETRYABLE\nrenewal_next_attempt: 2027-05-13T12:04:00Z\n"
		}},
		{"unsupported", renewDay(210), func(h *renewalHost) { h.writeUnsupported("1") }, func(*renewalHost) string {
			return "certificate_days_left: 90\ncertificate_state: renewal-due\ncertificate_renewal: unsupported by server\n"
		}},
		{"expired", renewDay(300), nil, func(h *renewalHost) string {
			return "certificate_days_left: 0\ncertificate_state: expired\nre_enroll: " + h.reenroll() + "\n"
		}},
	}
	for _, row := range rows {
		h := newRenewalHost(t)
		if row.state != nil {
			row.state(h)
		}
		code, stdout, stderr := h.statusAt(row.now)
		if want := statusHead + row.tail(h); code != 0 || stdout != want || stderr != "" {
			t.Errorf("%s: code = %d, stdout = %q, stderr = %q, want %q", row.name, code, stdout, stderr, want)
		}
	}
}

// Scenario: Дни до истечения — целые сутки с округлением вниз
func TestDaysLeftAreWholeDaysRoundedDown(t *testing.T) {
	t.Parallel()
	for now, want := range map[time.Duration]string{
		289*24*time.Hour + time.Hour:    "10",
		299*24*time.Hour + 19*time.Hour: "0",
		400 * 24 * time.Hour:            "0",
	} {
		h := newRenewalHost(t)
		_, stdout, _ := h.statusAt(renewT0.Add(now))
		if !strings.Contains(stdout, "certificate_days_left: "+want+"\n") {
			t.Errorf("at %v: stdout = %q, want %s days", now, stdout, want)
		}
	}
}

// Scenario: Файл состояния, не относящийся к сертификату, не учитывается
func TestAStateFileThatDoesNotConcernTheCertificateIsIgnored(t *testing.T) {
	t.Parallel()
	rows := map[string]func(h *renewalHost) func(string) ([]byte, error){
		"another serial": func(h *renewalHost) func(string) ([]byte, error) { h.writeState("ff"); return os.ReadFile },
		"not JSON": func(h *renewalHost) func(string) ([]byte, error) {
			h.writeState("1")
			if err := os.WriteFile(filepath.Join(h.stateDir, certrenew.StateFile), []byte("not json"), 0o600); err != nil {
				t.Fatal(err)
			}
			return os.ReadFile
		},
		"unreadable": func(h *renewalHost) func(string) ([]byte, error) {
			h.writeState("1")
			return func(name string) ([]byte, error) {
				if strings.HasSuffix(name, certrenew.StateFile) {
					return nil, os.ErrPermission
				}
				return os.ReadFile(name)
			}
		},
	}
	for name, setup := range rows {
		h := newRenewalHost(t)
		read := setup(h)
		code, stdout, stderr := h.statusWith(renewDay(210), read)
		if code != 0 || !strings.Contains(stdout, "certificate_state: renewal-due\n") || stderr != "" || strings.Contains(stdout, "renewal_failures") {
			t.Errorf("%s: code = %d, stdout = %q, stderr = %q", name, code, stdout, stderr)
		}
	}
}

// Scenario: status не читает ключ и файлы новой пары
func TestStatusReadsNeitherTheKeyNorTheNewPair(t *testing.T) {
	t.Parallel()
	h := newRenewalHost(t)
	h.install(h.issuePair(2, renewDay(200), renewDay(500)), ".new")
	if err := os.Remove(h.path("agent.key")); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := h.statusAt(renewDay(10))
	if code != 0 || !strings.Contains(stdout, "certificate_not_after: 2027-08-11T12:00:00Z\n") {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
}

func (h *renewalHost) writeUnsupported(serial string) {
	h.t.Helper()
	st := certrenew.State{Serial: serial, LastError: "Unimplemented", LastAttempt: renewDay(209), NextAttempt: renewDay(210), Unsupported: true}
	if err := os.MkdirAll(h.stateDir, 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := certrenew.WriteState(certpair.OSFS{}, filepath.Join(h.stateDir, certrenew.StateFile), st); err != nil {
		h.t.Fatal(err)
	}
}
