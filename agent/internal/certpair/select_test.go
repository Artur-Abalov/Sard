// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certpair_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/certtest"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/tlsid"
)

// docs/specs/agent/agent-certificate-renewal.feature, P21.

var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

const (
	tenant = "11111111-1111-1111-1111-111111111111"
	agent  = "22222222-2222-2222-2222-222222222222"
)

func day(n int) time.Time { return t0.AddDate(0, 0, n) }

type host struct {
	t   *testing.T
	ca  *certtest.CA
	dir string
}

func newHost(t *testing.T) *host {
	return &host{t: t, ca: certtest.NewCA(t), dir: t.TempDir()}
}

func (h *host) files() config.TLS {
	return config.TLS{CAFile: h.path("ca.pem"), KeyFile: h.path("agent.key"), CertFile: h.path("agent.pem")}
}

func (h *host) path(name string) string { return filepath.Join(h.dir, name) }

// pair issues a pair of the agent (or of another agent) valid from..to.
func (h *host) pair(serial int64, from, to time.Time) certtest.Pair {
	return h.ca.NewPair(h.t, certtest.Spec{Tenant: tenant, Agent: agent, Role: "source", Serial: serial, NotBefore: from, NotAfter: to})
}

func (h *host) write(name string, data []byte) {
	h.t.Helper()
	if err := os.WriteFile(h.path(name), data, 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *host) install(p certtest.Pair, suffix string) {
	h.write("agent.key"+suffix, p.KeyPEM)
	h.write("agent.pem"+suffix, p.CertPEM)
}

func (h *host) selectAt(now time.Time) (certpair.Selection, error) {
	return certpair.Select(h.files(), certpair.OSFS{}, now)
}

// Scenario: Выбирается годная пара с самым поздним NotBefore
func TestTheLatestIssuedUsablePairIsSelected(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(300)), "")
	h.install(h.pair(2, day(200), day(500)), ".new")
	got, err := h.selectAt(day(201))
	if err != nil {
		t.Fatal(err)
	}
	if got.Pair != (certpair.Pair{KeyFile: h.path("agent.key.new"), CertFile: h.path("agent.pem.new")}) || !got.KeyIsNew || !got.CertIsNew || !got.Pending() {
		t.Fatalf("selection = %+v", got)
	}
	if want := (certpair.Cert{Serial: "2", NotBefore: day(200), NotAfter: day(500)}); got.Cert != want || len(got.Stale) != 0 {
		t.Fatalf("cert = %+v, stale = %v", got.Cert, got.Stale)
	}
}

func (h *host) requireChosen(got certpair.Selection, err error, serial string, stale ...string) {
	h.t.Helper()
	if err != nil || got.Cert.Serial != serial || got.Expired != nil {
		h.t.Fatalf("selection = %+v, err = %v, want serial %s", got, err, serial)
	}
	want := map[string]bool{}
	for _, s := range stale {
		want[h.path(s)] = true
	}
	if len(got.Stale) != len(want) {
		h.t.Fatalf("stale = %v, want %v", got.Stale, stale)
	}
	for _, s := range got.Stale {
		if !want[s] {
			h.t.Fatalf("stale = %v, want %v", got.Stale, stale)
		}
	}
}

// Scenario: Выбирается годная пара с самым поздним NotBefore
func TestAnExpiredNewPairIsNeverSelectedAndIsStale(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(300)), "")
	h.install(h.pair(2, day(10), day(20)), ".new")
	got, err := h.selectAt(day(30))
	h.requireChosen(got, err, "1", "agent.key.new", "agent.pem.new")
}

// Scenario: Выбирается годная пара с самым поздним NotBefore
func TestANewPairOfAnotherAgentIsNeverSelectedAndIsStale(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(300)), "")
	other := h.ca.NewPair(t, certtest.Spec{Tenant: tenant, Agent: "33333333-3333-3333-3333-333333333333", Role: "source", Serial: 2, NotBefore: day(100), NotAfter: day(400)})
	h.install(other, ".new")
	got, err := h.selectAt(day(120))
	h.requireChosen(got, err, "1", "agent.key.new", "agent.pem.new")
}

// Scenario: Выбирается годная пара с самым поздним NotBefore
func TestAUsableNewPairIssuedBeforeTheCurrentOneStaysInPlace(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, day(10), day(300)), "")
	h.install(h.pair(2, t0, day(300)), ".new")
	got, err := h.selectAt(day(20))
	h.requireChosen(got, err, "1")
	if got.Pending() {
		t.Fatalf("selection = %+v", got)
	}
}

// Scenario: Выбирается годная пара с самым поздним NotBefore
func TestAnExpiredCurrentPairGivesWayToTheNewOne(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(10)), "")
	h.install(h.pair(2, day(5), day(500)), ".new")
	got, err := h.selectAt(day(12))
	h.requireChosen(got, err, "2")
	if !got.Pending() {
		t.Fatalf("selection = %+v", got)
	}
}

func TestTheCurrentPairWinsATieOfNotBefore(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(300)), "")
	h.install(h.pair(2, t0, day(500)), ".new")
	got, err := h.selectAt(day(1))
	h.requireChosen(got, err, "1")
}

// Scenario: После уменьшения срока выбирается новая пара, хотя она истекает раньше старой
func TestANewPairThatExpiresBeforeTheCurrentOneIsSelectedWhenIssuedLater(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(300)), "")
	h.install(h.pair(2, day(200), day(230)), ".new")
	got, err := h.selectAt(day(201))
	h.requireChosen(got, err, "2")
	if !got.Pending() {
		t.Fatalf("selection = %+v", got)
	}
}

// Scenario: Перезапуск между двумя переименованиями
func TestARestartBetweenTheTwoRenamesSelectsTheKeyAndTheNewCertificate(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	p2 := h.pair(2, day(200), day(500))
	h.write("agent.key", p2.KeyPEM)
	h.write("agent.pem", h.pair(1, t0, day(300)).CertPEM)
	h.write("agent.pem.new", p2.CertPEM)
	got, err := h.selectAt(day(201))
	h.requireChosen(got, err, "2")
	if got.KeyIsNew || !got.CertIsNew || got.Pair.KeyFile != h.path("agent.key") || got.Pair.CertFile != h.path("agent.pem.new") {
		t.Fatalf("selection = %+v", got)
	}
}

// Scenario: Недописанная новая пара удаляется, агент работает с текущей
func TestAnIncompleteNewPairAndTempFilesAreStale(t *testing.T) {
	t.Parallel()
	rows := map[string]func(h *host){
		"only key": func(h *host) { h.write("agent.key.new", h.pair(2, day(1), day(9)).KeyPEM) },
		"mismatch": func(h *host) {
			h.write("agent.key.new", h.pair(2, day(1), day(9)).KeyPEM)
			h.write("agent.pem.new", h.pair(3, day(1), day(9)).CertPEM)
		},
		"empty": func(h *host) {
			h.write("agent.key.new", h.pair(2, day(1), day(9)).KeyPEM)
			h.write("agent.pem.new", nil)
		},
		"temp only": func(h *host) { h.write(certpair.TempPrefix+"123", []byte("x")) },
	}
	for name, setup := range rows {
		h := newHost(t)
		h.install(h.pair(1, t0, day(300)), "")
		setup(h)
		got, err := h.selectAt(day(2))
		if err != nil || got.Cert.Serial != "1" || got.Pending() {
			t.Fatalf("%s: selection = %+v, err = %v", name, got, err)
		}
		if len(got.Stale) != len(h.extraFiles()) {
			t.Fatalf("%s: stale = %v, files = %v", name, got.Stale, h.extraFiles())
		}
	}
}

// extraFiles are the files of the directory besides the current pair.
func (h *host) extraFiles() []string {
	entries, _ := os.ReadDir(h.dir)
	var left []string
	for _, e := range entries {
		if e.Name() != "agent.key" && e.Name() != "agent.pem" {
			left = append(left, e.Name())
		}
	}
	return left
}

// Scenario: Без единой согласованной пары агент сообщает о незавершённой регистрации
func TestWithoutAConsistentPairSelectionReportsAnIncompleteEnrollment(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.write("agent.key", h.pair(3, t0, day(300)).KeyPEM)
	h.write("agent.pem", h.pair(1, t0, day(300)).CertPEM)
	_, err := h.selectAt(day(1))
	if !errors.Is(err, tlsid.ErrIncompleteEnrollment) {
		t.Fatalf("err = %v", err)
	}
}

func TestAMissingCertificateIsTheFileError(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	_, err := h.selectAt(day(1))
	var fe *tlsid.FileError
	if !errors.As(err, &fe) || fe.Key != "tls.cert_file" {
		t.Fatalf("err = %v", err)
	}
}

// Scenario: Истёкший сертификат останавливает агента до подключения с кодом 78
func TestWhenEveryConsistentPairExpiredSelectionSaysWhen(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(300)), "")
	got, err := h.selectAt(day(300))
	if err != nil || got.Expired == nil || !got.Expired.NotAfter.Equal(day(300)) || got.Expired.CertFile != h.path("agent.pem") {
		t.Fatalf("selection = %+v, err = %v", got, err)
	}
	want := `CERT_EXPIRED: tls.cert_file "` + h.path("agent.pem") + `" expired at 2027-08-11T12:00:00Z`
	if got.Expired.Error() != want || got.Pair.KeyFile != h.path("agent.key") {
		t.Fatalf("error = %q, pair = %+v", got.Expired.Error(), got.Pair)
	}
}

func TestOneSecondBeforeTheExpiryThePairIsUsable(t *testing.T) {
	t.Parallel()
	h := newHost(t)
	h.install(h.pair(1, t0, day(300)), "")
	if got, err := h.selectAt(day(300).Add(-time.Second)); err != nil || got.Expired != nil {
		t.Fatalf("selection = %+v, err = %v", got, err)
	}
}

func TestWithoutTLSPathsThereIsNothingToSelect(t *testing.T) {
	t.Parallel()
	got, err := certpair.Select(config.TLS{}, certpair.OSFS{}, t0)
	if err != nil || got.Pending() || got.Expired != nil {
		t.Fatalf("selection = %+v, err = %v", got, err)
	}
}
