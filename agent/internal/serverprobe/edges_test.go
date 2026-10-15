// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package serverprobe_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/serverprobe"
)

// The boundaries and the telling apart of failures that the main tests
// leave open.

func TestAReasonOfTheLongestAllowedLengthIsPassedOnAndOneLongerIsNot(t *testing.T) {
	sard := newCA(t, "sard")
	forty := strings.Repeat("A", 40)
	w := startServer(t, sard, sard, refusedWith(forty))
	if f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1")); f.Answer.Reason != forty {
		t.Errorf("40 characters: %+v", f.Answer)
	}
	w = startServer(t, sard, sard, refusedWith(forty+"A"))
	if f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1")); f.Answer.Reason != "" {
		t.Errorf("41 characters: %+v", f.Answer)
	}
}

func TestAnErrorOfTheNetworkIsATimeoutOnlyWhenItIsOne(t *testing.T) {
	sard := newCA(t, "sard")
	cases := []struct {
		name string
		err  error
		want hostcheck.FailKind
	}{
		{"a name server that is silent", &net.DNSError{Err: "i/o timeout", Name: "x", IsTimeout: true}, hostcheck.FailTimeout},
		{"a name server that fails", &net.DNSError{Err: "server misbehaving", Name: "x"}, hostcheck.FailOther},
		{"an operation that fails", &net.OpError{Op: "dial", Err: errors.New("network is unreachable")}, hostcheck.FailOther},
		{"an operation that times out", &net.OpError{Op: "dial", Err: context.DeadlineExceeded}, hostcheck.FailTimeout},
	}
	for _, c := range cases {
		req := serverprobe.Request{Address: "x:1", TLS: &tls.Config{RootCAs: sard.pool}, Timeout: time.Second, Now: time.Now,
			Dial: func(context.Context, string, string) (net.Conn, error) { return nil, c.err }}
		if f := serverprobe.Run(context.Background(), req); f.Reach.Kind != c.want {
			t.Errorf("%s: %+v", c.name, f.Reach)
		}
	}
}

func TestAServerThatStopsAnsweringBetweenTheStepsFailsTheHandshake(t *testing.T) {
	sard := newCA(t, "sard")
	w := startServer(t, sard, sard, okServer(epoch))
	req := w.request(t, sard, "127.0.0.1")
	real := (&net.Dialer{}).DialContext
	var calls atomic.Int32
	req.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if calls.Add(1) == 2 {
			return nil, errors.New("network is unreachable")
		}
		return real(ctx, network, address)
	}
	f := serverprobe.Run(context.Background(), req)
	if f.Reach.Kind != hostcheck.FailNone || f.TLS.Kind != hostcheck.FailOther || f.TLS.Detail != "network is unreachable" || w.server.probes.Load() != 0 {
		t.Errorf("%+v", f)
	}
}

// tlsServer accepts connections, handshakes them with cfg and hands them to
// serve, on its own; it is closed with the test.
func tlsServer(t *testing.T, cfg *tls.Config, serve func(c *tls.Conn)) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			raw, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = raw.Close() }()
				c := tls.Server(raw, cfg)
				if c.Handshake() == nil {
					serve(c)
				}
			}()
		}
	}()
	return l.Addr().String()
}

func serverConfig(t *testing.T, sard ca, modify func(*tls.Config)) *tls.Config {
	t.Helper()
	cfg := &tls.Config{Certificates: []tls.Certificate{sard.issue(t, x509.ExtKeyUsageServerAuth)}, MinVersion: tls.VersionTLS12}
	if modify != nil {
		modify(cfg)
	}
	return cfg
}

func TestAServerThatRejectsTheCertificateOfTheAgentAfterAWhileIsStillToldApart(t *testing.T) {
	sard := newCA(t, "sard")
	addr := tlsServer(t, serverConfig(t, sard, func(c *tls.Config) {
		c.ClientAuth = tls.RequireAnyClientCert
		c.VerifyPeerCertificate = func([][]byte, [][]*x509.Certificate) error {
			time.Sleep(200 * time.Millisecond)
			return errors.New("not mine")
		}
	}), func(*tls.Conn) {})
	w := world{sardCA: sard, addr: addr}
	f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1"))
	if f.TLS.Kind != hostcheck.FailClientCert {
		t.Errorf("%+v", f)
	}
}

func TestAServerThatRejectsTheCertificateOfTheAgentInTheHandshakeIsToldApart(t *testing.T) {
	sard := newCA(t, "sard")
	addr := tlsServer(t, serverConfig(t, sard, func(c *tls.Config) {
		c.ClientAuth = tls.RequireAnyClientCert
		c.VerifyPeerCertificate = func([][]byte, [][]*x509.Certificate) error { return errors.New("not mine") }
	}), func(*tls.Conn) {})
	w := world{sardCA: sard, addr: addr}
	req := w.request(t, sard, "127.0.0.1")
	req.TLS.MaxVersion = tls.VersionTLS12 // before TLS 1.3 the alert is part of the handshake
	if f := serverprobe.Run(context.Background(), req); f.TLS.Kind != hostcheck.FailClientCert {
		t.Errorf("%+v", f.TLS)
	}
}

func TestAnAlertThatIsNotAboutACertificateIsNotTakenForOne(t *testing.T) {
	sard := newCA(t, "sard")
	addr := tlsServer(t, serverConfig(t, sard, func(c *tls.Config) { c.MinVersion = tls.VersionTLS13 }), func(*tls.Conn) {})
	w := world{sardCA: sard, addr: addr}
	req := w.request(t, sard, "127.0.0.1")
	req.TLS.MaxVersion = tls.VersionTLS12
	f := serverprobe.Run(context.Background(), req)
	if f.TLS.Kind != hostcheck.FailOther || !strings.Contains(f.TLS.Detail, "protocol version") {
		t.Errorf("%+v", f.TLS)
	}
}

func TestACertificateThatIsNotValidIsNotTakenForARejectionOfTheClient(t *testing.T) {
	sard := newCA(t, "sard")
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(9), Subject: pkix.Name{CommonName: "old"}, NotBefore: epoch.Add(-48 * time.Hour),
		NotAfter: epoch.Add(-24 * time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, sard.cert, &key.PublicKey, sard.key)
	must(t, err)
	addr := tlsServer(t, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}, func(*tls.Conn) {})
	w := world{sardCA: sard, addr: addr}
	f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1"))
	if f.TLS.Kind != hostcheck.FailOther || !strings.Contains(f.TLS.Detail, "expired") {
		t.Errorf("%+v", f.TLS)
	}
}

func TestAServerThatSaysNothingAfterTheHandshakeIsNotAFailureOfTheHandshake(t *testing.T) {
	sard := newCA(t, "sard")
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	addr := tlsServer(t, serverConfig(t, sard, nil), func(*tls.Conn) { <-release })
	w := world{sardCA: sard, addr: addr}
	req := w.request(t, sard, "127.0.0.1")
	req.Timeout = 300 * time.Millisecond
	done := make(chan hostcheck.ServerFacts, 1)
	go func() { done <- serverprobe.Run(context.Background(), req) }()
	select {
	case f := <-done:
		if f.Reach.Kind != hostcheck.FailNone || f.TLS.Kind != hostcheck.FailNone {
			t.Errorf("%+v", f)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handshake waits for a server that has nothing to say")
	}
}

func TestAServerThatClosesAfterTheHandshakeIsNotARejectionOfTheClient(t *testing.T) {
	sard := newCA(t, "sard")
	addr := tlsServer(t, serverConfig(t, sard, nil), func(*tls.Conn) {})
	w := world{sardCA: sard, addr: addr}
	f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1"))
	if f.TLS.Kind == hostcheck.FailClientCert {
		t.Errorf("%+v", f.TLS)
	}
}

// counted wraps a connection and counts that it is closed.
type counted struct {
	net.Conn
	closed *atomic.Int32
	once   sync.Once
}

func (c *counted) Close() error {
	c.once.Do(func() { c.closed.Add(1) })
	return c.Conn.Close()
}

func TestEveryConnectionTheProbeOpensIsClosed(t *testing.T) {
	sard := newCA(t, "sard")
	w := startServer(t, sard, sard, okServer(epoch))
	req := w.request(t, sard, "127.0.0.1")
	real := (&net.Dialer{}).DialContext
	var opened, closed atomic.Int32
	req.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, err := real(ctx, network, address)
		if err != nil {
			return nil, err
		}
		opened.Add(1)
		return &counted{Conn: c, closed: &closed}, nil
	}
	if f := serverprobe.Run(context.Background(), req); f.Answer.Kind != hostcheck.AnswerAccepted {
		t.Fatalf("%+v", f)
	}
	deadline := time.Now().Add(3 * time.Second)
	for closed.Load() != opened.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if opened.Load() < 3 || closed.Load() != opened.Load() {
		t.Errorf("%d connections opened, %d closed", opened.Load(), closed.Load())
	}
}
