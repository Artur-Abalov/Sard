// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mathrand "math/rand/v2"
	"sync"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/tlsid"
)

// Caller makes the two calls of a renewal, each over a connection of its own
// that presents the given pair (transport.CertClient). An answer of the server
// comes back as a *CallError.
type Caller interface {
	Renew(ctx context.Context, certFile, keyFile string, csrDER []byte) (chainPEM string, err error)
	Confirm(ctx context.Context, certFile, keyFile string) error
}

// Clock is the agent's clock (transport.Clock).
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// Options are the Manager's dependencies; zero Rand, Random and Logger are
// the production ones.
type Options struct {
	// Files are tls.*: the current pair, renewed in place.
	Files config.TLS
	// StatePath is <executor.state_dir>/cert-renewal.json.
	StatePath string
	FS        certpair.FS
	Caller    Caller
	Clock     Clock
	// Rand returns a number in [0, 1) for the jitter of the pauses.
	Rand func() float64
	// Random is the entropy of new keys and CSRs.
	Random io.Reader
	// Hostname is only a label in the CSR; the server ignores it.
	Hostname string
	Logger   *slog.Logger
	// ReconnectDelay is the pause after the n-th failed confirmation at start
	// (from 0); the transport's reconnect delays (transport.Backoff). Nil is a minute.
	ReconnectDelay func(attempt int) time.Duration
}

// Manager renews the certificate of this agent. Run it once; its Registered
// method tells it that the agent has registered.
type Manager struct {
	o          Options
	registered chan struct{}
	once       sync.Once

	mu      sync.Mutex
	old     string
	pending *pendingPair
	// renamed counts the renewals of this process; reported, how many of them
	// Superseded has answered for.
	renamed, reported int
	failures          int
	unsupportedLogged bool
}

// New builds a Manager.
func New(o Options) *Manager {
	if o.Rand == nil {
		o.Rand = mathrand.Float64
	}
	if o.Random == nil {
		o.Random = rand.Reader
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Manager{o: o, registered: make(chan struct{})}
}

// Registered tells the Manager that Register succeeded; safe to call on every
// Register. Nothing is renewed before the first one (П17).
func (m *Manager) Registered() { m.once.Do(func() { close(m.registered) }) }

// Run renews until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-m.registered:
	}
	wait := m.untilDue()
	for m.sleep(ctx, wait) {
		wait = m.attempt(ctx)
	}
}

// untilDue is how long to wait for the threshold of the current certificate.
func (m *Manager) untilDue() time.Duration {
	cert, err := tlsid.ReadCertificate(m.o.Files.CertFile, m.o.FS.ReadFile)
	if err != nil {
		return time.Hour
	}
	return Threshold(cert.NotBefore, cert.NotAfter).Sub(m.o.Clock.Now())
}

// sleep waits d and reports whether ctx is still alive; no wait for d <= 0.
func (m *Manager) sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-m.o.Clock.After(d):
		return true
	}
}

// attempt tries once and returns how long to wait for the next one.
func (m *Manager) attempt(ctx context.Context) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.renew(ctx)
	switch {
	case err == nil:
		return m.succeeded()
	case errors.Is(err, errNoRenewal):
		return m.unsupported()
	}
	return m.failed(err)
}

// errNoRenewal: the server answers RenewCertificate with UNIMPLEMENTED.
var errNoRenewal = errors.New("the server does not support certificate renewal")

// unsupportedPause is how often an agent asks a server that cannot renew.
const unsupportedPause = 24 * time.Hour

// unsupported logs the missing feature once per process and records it for
// `sard-agent status`; it is no failure of the agent, so the failure count stays.
func (m *Manager) unsupported() time.Duration {
	if !m.unsupportedLogged {
		m.unsupportedLogged = true
		m.o.Logger.Warn("the server does not support certificate renewal; retrying every 24h")
	}
	m.save(State{Failures: m.failures, LastError: "Unimplemented", Unsupported: true}, unsupportedPause)
	return unsupportedPause
}

// failed counts a failed attempt, records it for `sard-agent status` and the
// log, and returns the pause before the next attempt (П19, П25, П28).
func (m *Manager) failed(err error) time.Duration {
	m.failures++
	delay := max(Delay(m.failures, m.o.Rand), retryAfter(err))
	st := m.save(State{Failures: m.failures, LastError: describe(err)}, delay)
	m.o.Logger.Warn("certificate renewal failed", "last_error", st.LastError, "failures", st.Failures, "next_attempt", st.NextAttempt.Format(time.RFC3339))
	return delay
}

// save fills in the certificate and the times of an attempt that is made now
// and has its next one after delay, and writes the state file.
func (m *Manager) save(st State, delay time.Duration) State {
	now := m.o.Clock.Now()
	st.Serial = m.serial()
	st.LastAttempt = now.UTC().Round(time.Second)
	st.NextAttempt = now.Add(delay).UTC().Round(time.Second)
	if err := WriteState(m.o.FS, m.o.StatePath, st); err != nil {
		m.o.Logger.Warn("writing the certificate renewal state failed", "error", describe(err))
	}
	return st
}

// serial is the serial number of the certificate being renewed, lowercase hex.
func (m *Manager) serial() string {
	cert, err := tlsid.ReadCertificate(m.o.Files.CertFile, m.o.FS.ReadFile)
	if err != nil {
		return ""
	}
	return cert.SerialNumber.Text(16)
}

// renew asks for a certificate unless one is waiting for its confirmation,
// then has the new pair confirmed and renamed into place.
func (m *Manager) renew(ctx context.Context) error {
	if m.pending == nil {
		if err := m.request(ctx); err != nil {
			return err
		}
	}
	return m.confirm(ctx)
}

// request gets a certificate on a new key and writes the new pair next to the
// current one; the pair then waits for its confirmation.
func (m *Manager) request(ctx context.Context) error {
	cur, err := tlsid.ReadCertificate(m.o.Files.CertFile, m.o.FS.ReadFile)
	if err != nil {
		return err
	}
	key, chain, err := m.ask(ctx)
	if err != nil {
		return err
	}
	keyPEM, err := marshalKey(key)
	if err != nil {
		return err
	}
	if err := certpair.WriteNew(m.o.Files, m.o.FS, keyPEM, []byte(chain)); err != nil {
		return err
	}
	m.pending = &pendingPair{old: cur.SerialNumber.Text(16), sel: certpair.Selection{Pair: certpair.NewPair(m.o.Files), KeyIsNew: true, CertIsNew: true}}
	return nil
}

// ask sends a CSR for a new key over the current pair and checks the answer.
func (m *Manager) ask(ctx context.Context) (*ecdsa.PrivateKey, string, error) {
	key, csr, err := m.newRequest()
	if err != nil {
		return nil, "", err
	}
	chain, err := m.o.Caller.Renew(ctx, m.o.Files.CertFile, m.o.Files.KeyFile, csr)
	if ce := callError(err); ce != nil && ce.Unsupported {
		return nil, "", errNoRenewal
	}
	if err != nil {
		return nil, "", err
	}
	return key, chain, m.checkResponse(chain, key)
}

// newRequest generates the new key (ECDSA P-256, like the key of enroll) and
// the CSR for it. The server ignores the subject; the host name only lets a
// human read the request in transit.
func (m *Manager) newRequest() (*ecdsa.PrivateKey, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), m.o.Random)
	if err != nil {
		return nil, nil, fmt.Errorf("generate the new key: %w", err)
	}
	csr, err := x509.CreateCertificateRequest(m.o.Random, &x509.CertificateRequest{
		Subject:            pkix.Name{CommonName: m.o.Hostname},
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create the CSR: %w", err)
	}
	return key, csr, nil
}

// pendingPair is a new pair on disk that the server has not confirmed yet.
type pendingPair struct {
	old string // serial of the certificate it replaces
	sel certpair.Selection
}

// confirm has the server confirm the new pair, over a connection that
// presents it, and renames it over the current one. A temporary refusal keeps
// the pair, so that only the confirmation is repeated; a final one removes it
// (П20).
func (m *Manager) confirm(ctx context.Context) error {
	p := m.pending
	err := m.o.Caller.Confirm(ctx, p.sel.Pair.CertFile, p.sel.Pair.KeyFile)
	if finalRefusal(err) {
		certpair.Discard(m.o.FS, m.o.Files)
		m.pending = nil
		return err
	}
	if err != nil {
		return err
	}
	if err := certpair.Promote(m.o.FS, m.o.Files, p.sel); err != nil {
		return err
	}
	m.old = p.old
	m.renamed++
	m.pending = nil
	return nil
}

// finalRefusal: the server will say the same to the same pair (П20).
func finalRefusal(err error) bool {
	ce := callError(err)
	return ce != nil && ce.Final
}

// retryAfter is the pause the server asked for, or 0.
func retryAfter(err error) time.Duration {
	if ce := callError(err); ce != nil {
		return ce.RetryAfter
	}
	return 0
}

// succeeded forgets the failures and plans the next renewal by the new certificate.
func (m *Manager) succeeded() time.Duration {
	m.failures = 0
	if err := RemoveState(m.o.FS, m.o.StatePath); err != nil {
		m.o.Logger.Warn("removing the certificate renewal state failed", "error", describe(err))
	}
	attrs := []any{"old_serial", m.old}
	if cert, err := tlsid.ReadCertificate(m.o.Files.CertFile, m.o.FS.ReadFile); err == nil {
		attrs = append(attrs, "new_serial", cert.SerialNumber.Text(16), "not_after", cert.NotAfter.UTC().Format(time.RFC3339))
	}
	m.o.Logger.Info("certificate renewed", attrs...)
	return m.untilDue()
}

func marshalKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal the new key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}
