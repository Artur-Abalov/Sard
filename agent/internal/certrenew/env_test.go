// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package certrenew_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
	"github.com/Artur-Abalov/sard/agent/internal/certtest"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/tlsid"
)

const (
	tenant = "11111111-1111-1111-1111-111111111111"
	agent  = "22222222-2222-2222-2222-222222222222"
)

func day(n int) time.Time { return t0.AddDate(0, 0, n) }

// logRecorder keeps the agent's log lines and lets a test wait for one.
type logRecorder struct {
	mu    sync.Mutex
	lines []string
	added chan struct{}
}

func newLogs() *logRecorder { return &logRecorder{added: make(chan struct{}, 1024)} }

func (r *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *logRecorder) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *logRecorder) WithGroup(string) slog.Handler            { return r }

func (r *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", rec.Level, rec.Message)
	rec.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	r.mu.Lock()
	r.lines = append(r.lines, b.String())
	r.mu.Unlock()
	r.added <- struct{}{}
	return nil
}

func (r *logRecorder) all() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

// waitFor blocks until a line contains want, and returns that line.
func (r *logRecorder) waitFor(t *testing.T, want string) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		r.mu.Lock()
		for _, l := range r.lines {
			if strings.Contains(l, want) {
				r.mu.Unlock()
				return l
			}
		}
		r.mu.Unlock()
		select {
		case <-r.added:
		case <-deadline:
			t.Fatalf("no log line contains %q; log:\n%s", want, r.all())
			return ""
		}
	}
}

// call is one RenewCertificate or ConfirmCertificate the fake server saw.
type call struct {
	kind string // "renew" or "confirm"
	// presented is the serial of the certificate the call was made with.
	presented string
	pub       *ecdsa.PublicKey // the CSR's key, for renew
}

// fakeServer answers the two calls as the test says. By default it issues a
// certificate on the key of the CSR that lives 300 days from now, for the
// agent of the presented certificate, and confirms.
type fakeServer struct {
	t  *testing.T
	ca *certtest.CA
	// clock gives NotBefore of the issued certificates.
	clock *certtest.Clock
	role  string

	mu      sync.Mutex
	calls   []call
	serial  int64
	renewFn func(n int, pub *ecdsa.PublicKey) (string, error)
	confirm func(n int) error
	issued  []string
	events  chan string
}

func (s *fakeServer) Renew(_ context.Context, certFile, keyFile string, csr []byte) (string, error) {
	req, err := x509.ParseCertificateRequest(csr)
	if err != nil {
		s.t.Errorf("the CSR does not parse: %v", err)
		return "", err
	}
	pub, _ := req.PublicKey.(*ecdsa.PublicKey)
	s.mu.Lock()
	s.calls = append(s.calls, call{kind: "renew", presented: serialOf(s.t, certFile), pub: pub})
	n := s.count("renew")
	s.mu.Unlock()
	s.events <- "renew"
	if s.renewFn != nil {
		return s.renewFn(n, pub)
	}
	return s.issuePlain(pub)
}

// issuePlain is the default answer: a certificate for 300 days from now.
func (s *fakeServer) issuePlain(pub *ecdsa.PublicKey) (string, error) {
	return s.issue(pub, s.clock.Now().Sub(t0), 300)
}

// issue signs pub as a certificate of the agent that starts at now and lives days.
func (s *fakeServer) issue(pub *ecdsa.PublicKey, since time.Duration, days int) (string, error) {
	s.mu.Lock()
	s.serial++
	serial := s.serial + 1
	s.mu.Unlock()
	from := t0.Add(since)
	cert, err := s.ca.Issue(pub, certtest.Spec{Tenant: tenant, Agent: agent, Role: s.role, Serial: serial, NotBefore: from, NotAfter: from.AddDate(0, 0, days)})
	if err == nil {
		s.mu.Lock()
		s.issued = append(s.issued, string(cert))
		s.mu.Unlock()
	}
	return string(cert), err
}

func (s *fakeServer) Confirm(_ context.Context, certFile, _ string) error {
	s.mu.Lock()
	s.calls = append(s.calls, call{kind: "confirm", presented: serialOf(s.t, certFile)})
	n := s.count("confirm")
	s.mu.Unlock()
	s.events <- "confirm"
	if s.confirm != nil {
		return s.confirm(n)
	}
	return nil
}

// count is how many calls of kind there have been; the caller holds mu.
func (s *fakeServer) count(kind string) int {
	n := 0
	for _, c := range s.calls {
		if c.kind == kind {
			n++
		}
	}
	return n
}

func (s *fakeServer) callsOf(kind string) []call {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []call
	for _, c := range s.calls {
		if c.kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// next waits for the next call and returns its kind.
func (s *fakeServer) next() string {
	select {
	case e := <-s.events:
		return e
	case <-time.After(5 * time.Second):
		s.t.Fatal("the agent made no call")
		return ""
	}
}

func serialOf(t *testing.T, certFile string) string {
	cert, err := tlsid.ReadCertificate(certFile, os.ReadFile)
	if err != nil {
		t.Errorf("the presented certificate: %v", err)
		return ""
	}
	return cert.SerialNumber.Text(16)
}

// env is an agent with pair 1 (certificate C1, serial 1, from T0 for 300
// days), a fake server and a clock at T0.
type env struct {
	t        *testing.T
	ca       *certtest.CA
	dir      string
	stateDir string
	clock    *certtest.Clock
	server   *fakeServer
	logs     *logRecorder
	p1       certtest.Pair
	fs       certpair.FS
	rnd      float64
	mgr      *certrenew.Manager
	cancel   context.CancelFunc
	done     chan struct{}
}

func newEnv(t *testing.T) *env { return newEnvFor(t, day(300)) }

// newEnvFor is newEnv whose pair 1 expires at notAfter.
func newEnvFor(t *testing.T, notAfter time.Time) *env {
	t.Helper()
	ca := certtest.NewCA(t)
	e := &env{t: t, ca: ca, dir: t.TempDir(), stateDir: t.TempDir(), clock: certtest.NewClock(t0), logs: newLogs(), fs: certpair.OSFS{}, rnd: 0.999999}
	e.server = &fakeServer{t: t, ca: ca, clock: e.clock, role: "source", events: make(chan string, 1024)}
	e.p1 = ca.NewPair(t, certtest.Spec{Tenant: tenant, Agent: agent, Role: "source", Serial: 1, NotBefore: t0, NotAfter: notAfter})
	e.write("ca.pem", ca.PEM())
	e.write("agent.key", e.p1.KeyPEM)
	e.write("agent.pem", e.p1.CertPEM)
	return e
}

func (e *env) path(name string) string { return filepath.Join(e.dir, name) }

func (e *env) files() config.TLS {
	return config.TLS{CAFile: e.path("ca.pem"), KeyFile: e.path("agent.key"), CertFile: e.path("agent.pem")}
}

func (e *env) statePath() string { return filepath.Join(e.stateDir, certrenew.StateFile) }

func (e *env) write(name string, data []byte) {
	e.t.Helper()
	if err := os.WriteFile(e.path(name), data, 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) read(name string) string {
	data, err := os.ReadFile(e.path(name))
	if err != nil {
		return ""
	}
	return string(data)
}

func (e *env) exists(name string) bool {
	_, err := os.Lstat(e.path(name))
	return err == nil
}

// names lists the files of the tls directory.
func (e *env) names() string {
	entries, err := os.ReadDir(e.dir)
	if err != nil {
		e.t.Fatal(err)
	}
	var names []string
	for _, en := range entries {
		names = append(names, en.Name())
	}
	return strings.Join(names, " ")
}

func (e *env) manager() *certrenew.Manager {
	return certrenew.New(certrenew.Options{
		Files: e.files(), StatePath: e.statePath(), FS: e.fs, Caller: e.server, Clock: e.clock,
		Rand: func() float64 { return e.rnd }, Hostname: "db1", Logger: slog.New(e.logs),
		ReconnectDelay: func(attempt int) time.Duration { return time.Duration(float64(time.Second<<attempt) * e.rnd) },
	})
}

// start runs the renewal as an agent that has just registered.
func (e *env) start() {
	e.t.Helper()
	e.startWith(e.manager())
	e.mgr.Registered()
}

// startWith runs mgr; the test ends only after Run has returned, so that no
// attempt still works on the files of a directory that is being removed.
func (e *env) startWith(mgr *certrenew.Manager) {
	e.t.Helper()
	e.mgr = mgr
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.done = make(chan struct{})
	go func() { mgr.Run(ctx); close(e.done) }()
	e.t.Cleanup(e.stop)
}

func (e *env) stop() {
	e.cancel()
	select {
	case <-e.done:
	case <-time.After(5 * time.Second):
		e.t.Error("the renewal did not stop")
	}
}

// idle waits until the renewal sleeps and returns how long.
func (e *env) idle() time.Duration { return e.clock.WaitTimer(e.t) }

// failingFS is the host file system whose writes of chosen files fail.
type failingFS struct {
	certpair.OSFS
	failWrite  string // base name of the file whose write fails
	dir        string
	failRename bool
}

func (f failingFS) WriteFile(path string, data []byte, mode os.FileMode) error {
	if filepath.Base(path) == f.failWrite {
		return &fs.PathError{Op: "open", Path: filepath.Join(f.dir, certpair.TempPrefix+"1"), Err: syscall.EACCES}
	}
	return f.OSFS.WriteFile(path, data, mode)
}

func (f failingFS) Rename(oldPath, newPath string) error {
	if f.failRename {
		return &fs.PathError{Op: "rename", Path: newPath, Err: syscall.EIO}
	}
	return f.OSFS.Rename(oldPath, newPath)
}
