// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/Artur-Abalov/sard/agent/internal/certtest"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Fixtures of docs/specs/agent/agent-certificate-renewal.feature: the agent
// runs whole against a fake AgentService over TLS, with a clock the test moves.

var renewT0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func renewDay(n int) time.Time { return renewT0.AddDate(0, 0, n) }

// renewalServer is AgentService and EnrollmentService on one TLS port. Every
// call is an event "<kind> C<serial>" or "<kind>"; a call also records the
// TCP connection it came by.
type renewalServer struct {
	agentv1.UnimplementedAgentServiceServer
	agentv1.UnimplementedEnrollmentServiceServer
	ca    *certtest.CA
	clock *certtest.Clock

	mu      sync.Mutex
	events  []string
	conns   map[string]string // event index -> connection (remote address)
	changed chan struct{}
	// streams are the open Connect calls; closing one ends it.
	streams []chan error
	hellos  []*agentv1.Hello
	serials int64

	// answers; nil is success
	register func(serial string, n int) error
	confirm  func(serial string, n int) error
	renew    func(serial string, n int, pub *ecdsa.PublicKey) (string, error)
	enroll   func(req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error)
}

func newRenewalServer(t *testing.T, ca *certtest.CA, clock *certtest.Clock) (*renewalServer, string) {
	t.Helper()
	s := &renewalServer{ca: ca, clock: clock, changed: make(chan struct{}, 1), conns: map[string]string{}}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	creds := credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{ca.Server(t, "127.0.0.1")}, ClientAuth: tls.RequestClientCert, MinVersion: tls.VersionTLS12})
	g := grpc.NewServer(grpc.Creds(creds))
	agentv1.RegisterAgentServiceServer(g, s)
	agentv1.RegisterEnrollmentServiceServer(g, s)
	go func() { _ = g.Serve(lis) }()
	t.Cleanup(g.Stop)
	return s, lis.Addr().String()
}

// note records an event of the call in ctx and returns how many events of its kind there have been.
func (s *renewalServer) note(ctx context.Context, kind string) (serial string, n int) {
	serial = presentedSerial(ctx)
	event := kind
	if serial != "" {
		event += " C" + serial
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if strings.HasPrefix(e, kind) {
			n++
		}
	}
	n++
	s.conns[fmt.Sprint(len(s.events))] = remoteOf(ctx)
	s.events = append(s.events, event)
	select {
	case s.changed <- struct{}{}:
	default:
	}
	return serial, n
}

func remoteOf(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok {
		return p.Addr.String()
	}
	return ""
}

// presentedSerial is the serial number of the client certificate of the call, lowercase hex.
func presentedSerial(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return ""
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok || len(info.State.PeerCertificates) == 0 {
		return ""
	}
	return info.State.PeerCertificates[0].SerialNumber.Text(16)
}

func (s *renewalServer) Register(ctx context.Context, _ *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error) {
	serial, n := s.note(ctx, "register")
	if s.register != nil {
		if err := s.register(serial, n); err != nil {
			return nil, err
		}
	}
	return &agentv1.RegisterResponse{HeartbeatInterval: durationpb.New(30 * time.Second)}, nil
}

func (s *renewalServer) Connect(stream grpc.BidiStreamingServer[agentv1.ConnectRequest, agentv1.ConnectResponse]) error {
	s.note(stream.Context(), "connect")
	end := make(chan error, 1)
	s.mu.Lock()
	s.streams = append(s.streams, end)
	s.mu.Unlock()
	hello, err := stream.Recv()
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.hellos = append(s.hellos, hello.GetHello())
	s.mu.Unlock()
	s.note(stream.Context(), "hello")
	go func() {
		for {
			if _, err := stream.Recv(); err != nil {
				return
			}
		}
	}()
	select {
	case err := <-end:
		return err
	case <-stream.Context().Done():
		return nil
	}
}

// dropStreams ends every open stream with UNAVAILABLE.
func (s *renewalServer) dropStreams() {
	s.mu.Lock()
	streams := s.streams
	s.streams = nil
	s.mu.Unlock()
	for _, end := range streams {
		end <- status.Error(14, "dropped by the test")
	}
}

func (s *renewalServer) ConfirmCertificate(ctx context.Context, _ *agentv1.ConfirmCertificateRequest) (*agentv1.ConfirmCertificateResponse, error) {
	serial, n := s.note(ctx, "confirm")
	if s.confirm != nil {
		if err := s.confirm(serial, n); err != nil {
			return nil, err
		}
	}
	return &agentv1.ConfirmCertificateResponse{}, nil
}

func (s *renewalServer) RenewCertificate(ctx context.Context, req *agentv1.RenewCertificateRequest) (*agentv1.RenewCertificateResponse, error) {
	serial, n := s.note(ctx, "renew")
	csr, err := x509.ParseCertificateRequest(req.GetCsrDer())
	if err != nil {
		return nil, status.Error(3, "bad CSR")
	}
	pub, _ := csr.PublicKey.(*ecdsa.PublicKey)
	if s.renew != nil {
		chain, err := s.renew(serial, n, pub)
		return &agentv1.RenewCertificateResponse{CertificateChainPem: chain}, err
	}
	chain, err := s.issue(pub, 300)
	return &agentv1.RenewCertificateResponse{CertificateChainPem: chain}, err
}

// issue signs pub as a certificate of the test agent from the clock's now for days.
func (s *renewalServer) issue(pub *ecdsa.PublicKey, days int) (string, error) {
	s.mu.Lock()
	s.serials++
	serial := s.serials + 1 // pair 1 is serial 1
	s.mu.Unlock()
	from := s.clock.Now()
	cert, err := s.ca.Issue(pub, certtest.Spec{Tenant: testTenant, Agent: testAgent, Role: "source", Serial: serial, NotBefore: from, NotAfter: from.AddDate(0, 0, days)})
	return string(cert), err
}

func (s *renewalServer) Enroll(ctx context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	s.note(ctx, "enroll")
	if s.enroll == nil {
		return nil, status.Error(12, "no enrollment")
	}
	return s.enroll(req)
}

// eventLog is the events so far.
func (s *renewalServer) eventLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

// count is how many events start with prefix.
func (s *renewalServer) count(prefix string) int {
	n := 0
	for _, e := range s.eventLog() {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

// waitCount waits until at least n events start with prefix.
func (s *renewalServer) waitCount(t *testing.T, prefix string, n int) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for s.count(prefix) < n {
		select {
		case <-s.changed:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatalf("the server saw %d x %q, want %d; events: %v", s.count(prefix), prefix, n, s.eventLog())
		}
	}
}

// connectionOf is the connection of the i-th event.
func (s *renewalServer) connectionOf(i int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns[fmt.Sprint(i)]
}

// logBuffer is the agent's log, as text.
type logBuffer struct{ syncBuffer }

func (l *logBuffer) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&l.syncBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// renewalHost is an agent host with pair 1 (serial 1, from T0 for 300 days),
// a fake server and a clock at T0.
type renewalHost struct {
	t        *testing.T
	ca       *certtest.CA
	dir      string
	stateDir string
	clock    *certtest.Clock
	server   *renewalServer
	address  string
	cfg      string
	log      *logBuffer
	p1       certtest.Pair
	rnd      float64
}

func newRenewalHost(t *testing.T) *renewalHost {
	t.Helper()
	useMachineIDFiles(t, "", "")
	ca := certtest.NewCA(t)
	h := &renewalHost{t: t, ca: ca, dir: t.TempDir(), stateDir: filepath.Join(t.TempDir(), "state"), clock: certtest.NewClock(renewT0), log: &logBuffer{}, rnd: 0.999999}
	h.server, h.address = newRenewalServer(t, ca, h.clock)
	h.p1 = h.issuePair(1, renewT0, renewDay(300))
	h.writeFile("ca.pem", ca.PEM(), 0o600)
	h.install(h.p1, "")
	h.cfg = writeConfig(t, "server:\n  address: "+h.address+"\n"+
		"tls: {ca_file: "+h.path("ca.pem")+", cert_file: "+h.path("agent.pem")+", key_file: "+h.path("agent.key")+"}\n"+
		"executor: {state_dir: "+h.stateDir+"}\n"+withRestic(t))
	return h
}

func (h *renewalHost) path(name string) string { return filepath.Join(h.dir, name) }

func (h *renewalHost) issuePair(serial int64, from, to time.Time) certtest.Pair {
	return h.ca.NewPair(h.t, certtest.Spec{Tenant: testTenant, Agent: testAgent, Role: "source", Serial: serial, NotBefore: from, NotAfter: to})
}

func (h *renewalHost) writeFile(name string, data []byte, mode os.FileMode) {
	h.t.Helper()
	writeFile(h.t, h.path(name), data, mode)
}

// install writes a pair as agent.key and agent.pem with the suffix.
func (h *renewalHost) install(p certtest.Pair, suffix string) {
	h.writeFile("agent.key"+suffix, p.KeyPEM, 0o600)
	h.writeFile("agent.pem"+suffix, p.CertPEM, 0o644)
}

func (h *renewalHost) read(name string) string {
	data, err := os.ReadFile(h.path(name))
	if err != nil {
		return ""
	}
	return string(data)
}

func (h *renewalHost) exists(name string) bool {
	_, err := os.Lstat(h.path(name))
	return err == nil
}

// names lists the files of the tls directory.
func (h *renewalHost) names() string {
	entries, err := os.ReadDir(h.dir)
	if err != nil {
		h.t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, " ")
}

// deps are the agent's dependencies for this host.
func (h *renewalHost) deps() selfEnrollDeps {
	d := selfDepsFor(h.clock)
	d.runtime = agentRuntime{clock: h.clock, rand: func() float64 { return h.rnd }, logger: h.log.logger()}
	return d
}

// runningAgent is an agent in a goroutine.
type runningAgent struct {
	cancel context.CancelFunc
	done   chan int
	stdout syncBuffer
	stderr syncBuffer
}

// run starts the agent with the flags, as "sard-agent --config C ...".
func (h *renewalHost) run(flags ...string) *runningAgent {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	a := &runningAgent{cancel: cancel, done: make(chan int, 1)}
	go func() {
		a.done <- runAgentCmd(ctx, append([]string{"--config", h.cfg}, flags...), &a.stdout, &a.stderr, fixedHostname, h.deps())
	}()
	h.t.Cleanup(func() { cancel(); a.wait(h.t) })
	return a
}

// wait returns the exit code once the agent has ended.
func (a *runningAgent) wait(t *testing.T) int {
	t.Helper()
	select {
	case code := <-a.done:
		a.done <- code
		return code
	case <-time.After(15 * time.Second):
		t.Fatalf("the agent did not end; stdout = %q, stderr = %q", a.stdout.String(), a.stderr.String())
		return -1
	}
}

// stop ends the agent as SIGTERM does and returns its code.
func (a *runningAgent) stop(t *testing.T) int {
	a.cancel()
	return a.wait(t)
}

// exited reports whether the agent has ended.
func (a *runningAgent) exited() bool {
	select {
	case code := <-a.done:
		a.done <- code
		return true
	default:
		return false
	}
}
