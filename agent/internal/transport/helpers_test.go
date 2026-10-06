// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// authority is a test CA shaped like the server's (ADR 0014): ECDSA P-256,
// server certificates with serverAuth and the server names as SANs, agent
// certificates with clientAuth and a sard:// URI SAN.
type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newAuthority(t *testing.T) *authority {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Sard CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &authority{cert: cert, key: key, pem: pemBlock("CERTIFICATE", der)}
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func pemBlock(kind string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
}

func (a *authority) issue(t *testing.T, tmpl *x509.Certificate) (certPEM, keyPEM []byte) {
	t.Helper()
	key := newKey(t)
	tmpl.SerialNumber = big.NewInt(time.Now().UnixNano())
	tmpl.NotBefore = time.Now().Add(-time.Hour)
	tmpl.NotAfter = time.Now().Add(24 * time.Hour)
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pemBlock("CERTIFICATE", der), pemBlock("EC PRIVATE KEY", keyDER)
}

// serverCert is for "localhost" and 127.0.0.1, like the default sard.pki.server-names.
func (a *authority) serverCert(t *testing.T) tls.Certificate {
	certPEM, keyPEM := a.issue(t, &x509.Certificate{
		Subject:     pkix.Name{CommonName: "localhost"},
		DNSNames:    []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return pair
}

// agentFiles writes an agent certificate, its key and the CA into dir, as
// enroll (A2) will, and returns the tls.* section pointing at them.
func (a *authority) agentFiles(t *testing.T, trust *authority) config.TLS {
	t.Helper()
	uri, _ := url.Parse("sard://tenants/t1/agents/a1")
	certPEM, keyPEM := a.issue(t, &x509.Certificate{
		URIs:        []*url.URL{uri},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	dir := t.TempDir()
	files := config.TLS{
		CAFile:   filepath.Join(dir, "ca.pem"),
		CertFile: filepath.Join(dir, "agent.pem"),
		KeyFile:  filepath.Join(dir, "agent.key"),
	}
	for path, data := range map[string][]byte{files.CAFile: trust.pem, files.CertFile: certPEM, files.KeyFile: keyPEM} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// fakeServer is AgentService over TLS with client-auth optional (ADR 0009):
// it records whether the agent presented a certificate its CA verifies.
type fakeServer struct {
	agentv1.UnimplementedAgentServiceServer
	addr string

	mu          sync.Mutex
	register    func() (*agentv1.RegisterResponse, error)
	registered  []*agentv1.RegisterRequest
	verified    []bool // per Register: client certificate verified by the CA
	connects    int
	closeStream error // what Connect returns when the call ends from the server side
	streams     chan *serverStream
}

// serverStream is one Connect call as the test sees it.
type serverStream struct {
	recv chan *agentv1.ConnectRequest
	send chan *agentv1.ConnectResponse
	done chan struct{} // close to end the call from the server side
}

func startServer(t *testing.T, ca *authority, serverCA *authority) *fakeServer {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	creds := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{serverCA.serverCert(t)}, // leaf only, like the server
		ClientCAs:    pool,
		ClientAuth:   tls.VerifyClientCertIfGiven,
		MinVersion:   tls.VersionTLS12,
	})
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{
		addr:    lis.Addr().String(), // the IP the server listens on: "localhost" may resolve to ::1 first
		streams: make(chan *serverStream, 16),
		register: func() (*agentv1.RegisterResponse, error) {
			return registered("agent-1", 30*time.Second), nil
		},
	}
	srv := grpc.NewServer(grpc.Creds(creds))
	agentv1.RegisterAgentServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return s
}

func (s *fakeServer) Register(ctx context.Context, req *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registered = append(s.registered, req)
	s.verified = append(s.verified, clientVerified(ctx))
	return s.register()
}

func (s *fakeServer) Connect(stream grpc.BidiStreamingServer[agentv1.ConnectRequest, agentv1.ConnectResponse]) error {
	s.mu.Lock()
	s.connects++
	s.mu.Unlock()
	ss := &serverStream{
		recv: make(chan *agentv1.ConnectRequest, 64),
		send: make(chan *agentv1.ConnectResponse, 16),
		done: make(chan struct{}),
	}
	s.streams <- ss
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				close(ss.recv)
				return
			}
			ss.recv <- msg
		}
	}()
	for {
		select {
		case msg := <-ss.send:
			if err := stream.Send(msg); err != nil {
				return err
			}
		case <-ss.done:
			return s.closeStream
		case <-stream.Context().Done():
			return nil
		}
	}
}

func (s *fakeServer) registerCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.registered)
}

func (s *fakeServer) connectCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connects
}

func registered(agentID string, heartbeat time.Duration) *agentv1.RegisterResponse {
	return &agentv1.RegisterResponse{AgentId: agentID, HeartbeatInterval: durationOf(heartbeat)}
}

// next waits for the next stream the agent opens.
func (s *fakeServer) next(t *testing.T) *serverStream {
	t.Helper()
	select {
	case ss := <-s.streams:
		return ss
	case <-time.After(3 * time.Second):
		t.Fatal("agent did not open a stream")
		return nil
	}
}

// read waits for the next message the agent sends on this stream.
func (ss *serverStream) read(t *testing.T) *agentv1.ConnectRequest {
	t.Helper()
	select {
	case msg, ok := <-ss.recv:
		if !ok {
			t.Fatal("stream closed")
		}
		return msg
	case <-time.After(3 * time.Second):
		t.Fatal("agent sent nothing")
		return nil
	}
}

// fakeClock fires timers only when the test advances it.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
	added  chan time.Duration // every After call, for the test to wait on
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC), added: make(chan time.Duration, 1024)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	ch := make(chan time.Time, 1)
	c.timers = append(c.timers, fakeTimer{at: c.now.Add(d), ch: ch})
	c.mu.Unlock()
	c.added <- d
	return ch
}

// Advance moves the clock and fires every timer that is due.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.timers[:0]
	for _, tm := range c.timers {
		if tm.at.After(c.now) {
			kept = append(kept, tm)
			continue
		}
		tm.ch <- c.now
	}
	c.timers = kept
}

// waitTimer returns the duration of the next After call.
func (c *fakeClock) waitTimer(t *testing.T) time.Duration {
	t.Helper()
	select {
	case d := <-c.added:
		return d
	case <-time.After(3 * time.Second):
		t.Fatal("no timer was set")
		return 0
	}
}

// commands records what the transport hands to the executor.
type commands struct {
	state     *state // Ack drops the result there, as the executor does
	mu        sync.Mutex
	submitted []string
	cancelled []string
	acked     []string
	events    chan string
}

func newCommands() *commands { return &commands{events: make(chan string, 64)} }

func (c *commands) Submit(step *agentv1.RunStep) {
	c.mu.Lock()
	c.submitted = append(c.submitted, step.GetCommandId())
	c.mu.Unlock()
	c.events <- "submit " + step.GetCommandId()
}

func (c *commands) Cancel(id string) {
	c.mu.Lock()
	c.cancelled = append(c.cancelled, id)
	c.mu.Unlock()
	c.events <- "cancel " + id
}

func (c *commands) Ack(id string) error {
	c.mu.Lock()
	c.acked = append(c.acked, id)
	c.mu.Unlock()
	if c.state != nil {
		c.state.mu.Lock()
		c.state.pending = slices.DeleteFunc(c.state.pending, func(r *agentv1.StepResult) bool { return r.GetCommandId() == id })
		c.state.mu.Unlock()
	}
	c.events <- "ack " + id
	return nil
}

func (c *commands) event(t *testing.T) string {
	t.Helper()
	select {
	case e := <-c.events:
		return e
	case <-time.After(3 * time.Second):
		t.Fatal("no command reached the executor")
		return ""
	}
}

// state is what the executor reports for Hello and resending.
type state struct {
	mu      sync.Mutex
	running []string
	pending []*agentv1.StepResult
}

func (s *state) RunningIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.running...)
}

func (s *state) PendingResults() []*agentv1.StepResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*agentv1.StepResult(nil), s.pending...)
}
