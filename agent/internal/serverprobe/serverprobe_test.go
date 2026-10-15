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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	"github.com/Artur-Abalov/sard/agent/internal/serverprobe"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	spb "google.golang.org/genproto/googleapis/rpc/status"
)

var epoch = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// ca is a test CA with the certificates it issued.
type ca struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newCA(t *testing.T, name string) ca {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, NotBefore: epoch.Add(-time.Hour),
		NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	must(t, err)
	cert, err := x509.ParseCertificate(der)
	must(t, err)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return ca{cert: cert, key: key, pool: pool}
}

func (c ca) issue(t *testing.T, usage x509.ExtKeyUsage) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "leaf"}, NotBefore: epoch.Add(-time.Hour),
		NotAfter: time.Now().Add(24 * time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{usage},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, c.cert, &key.PublicKey, c.key)
	must(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// fakeServer is an AgentService that counts what the agent calls.
type fakeServer struct {
	agentv1.UnimplementedAgentServiceServer
	probe             func() (*agentv1.ProbeResponse, error)
	probes, registers atomic.Int32
	connects          atomic.Int32
}

func (s *fakeServer) Probe(context.Context, *agentv1.ProbeRequest) (*agentv1.ProbeResponse, error) {
	s.probes.Add(1)
	return s.probe()
}

func (s *fakeServer) Register(context.Context, *agentv1.RegisterRequest) (*agentv1.RegisterResponse, error) {
	s.registers.Add(1)
	return nil, status.Error(codes.Unimplemented, "")
}

func (s *fakeServer) Connect(grpc.BidiStreamingServer[agentv1.ConnectRequest, agentv1.ConnectResponse]) error {
	s.connects.Add(1)
	return status.Error(codes.Unimplemented, "")
}

type world struct {
	sardCA ca
	server *fakeServer
	addr   string
}

// startServer serves AgentService with mTLS: the server certificate and the
// clients the server trusts are the arguments.
func startServer(t *testing.T, serverCA, clientCA ca, srv *fakeServer) world {
	t.Helper()
	cfg := &tls.Config{Certificates: []tls.Certificate{serverCA.issue(t, x509.ExtKeyUsageServerAuth)},
		ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCA.pool, MinVersion: tls.VersionTLS12}
	g := grpc.NewServer(grpc.Creds(credentials.NewTLS(cfg)))
	agentv1.RegisterAgentServiceServer(g, srv)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	go func() { _ = g.Serve(l) }()
	t.Cleanup(g.Stop)
	return world{sardCA: serverCA, server: srv, addr: l.Addr().String()}
}

func okServer(at time.Time) *fakeServer {
	return &fakeServer{probe: func() (*agentv1.ProbeResponse, error) {
		return &agentv1.ProbeResponse{ServerTime: timestamppb.New(at)}, nil
	}}
}

func (w world) request(t *testing.T, agentCA ca, serverName string) serverprobe.Request {
	t.Helper()
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: w.sardCA.pool, ServerName: serverName,
		Certificates: []tls.Certificate{agentCA.issue(t, x509.ExtKeyUsageClientAuth)}}
	return serverprobe.Request{Address: w.addr, TLS: cfg, Timeout: 2 * time.Second, Now: func() time.Time { return epoch }}
}

func TestAnAcceptedAgentGetsTheTimeOfTheServer(t *testing.T) {
	sard := newCA(t, "sard")
	w := startServer(t, sard, sard, okServer(epoch.Add(5*time.Second)))
	f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1"))
	if f.Reach.Kind != hostcheck.FailNone || f.TLS.Kind != hostcheck.FailNone || f.Answer.Kind != hostcheck.AnswerAccepted {
		t.Fatalf("%+v", f)
	}
	if !f.ServerTime.Equal(epoch.Add(5*time.Second)) || !f.Sent.Equal(epoch) || f.RTT <= 0 {
		t.Errorf("times: %+v", f)
	}
}

func TestOnlyProbeIsCalledNeverRegisterOrConnect(t *testing.T) {
	sard := newCA(t, "sard")
	srv := okServer(epoch)
	w := startServer(t, sard, sard, srv)
	serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1"))
	if got := [3]int32{srv.probes.Load(), srv.registers.Load(), srv.connects.Load()}; got != [3]int32{1, 0, 0} {
		t.Errorf("probes, registers, connects = %v", got)
	}
}

func refusedWith(reason string) *fakeServer { return refusedIn("sard.dev", reason) }

func refusedIn(domain, reason string) *fakeServer {
	return &fakeServer{probe: func() (*agentv1.ProbeResponse, error) {
		info, _ := anypb.New(&errdetails.ErrorInfo{Reason: reason, Domain: domain})
		return nil, status.ErrorProto(&spb.Status{Code: int32(codes.Unauthenticated), Message: "agent certificate rejected", Details: []*anypb.Any{info}})
	}}
}

func TestTheReasonOfARefusalIsPassedOn(t *testing.T) {
	sard := newCA(t, "sard")
	w := startServer(t, sard, sard, refusedWith("CERT_REVOKED"))
	f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1"))
	if f.Answer.Kind != hostcheck.AnswerRefused || f.Answer.Reason != "CERT_REVOKED" {
		t.Errorf("%+v", f.Answer)
	}
}

func TestAReasonThatIsNotPlainCapitalsIsNotCopied(t *testing.T) {
	sard := newCA(t, "sard")
	w := startServer(t, sard, sard, refusedWith("bad\nreason"))
	f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1"))
	if f.Answer.Kind != hostcheck.AnswerRefused || f.Answer.Reason != "" {
		t.Errorf("%+v", f.Answer)
	}
	w = startServer(t, sard, sard, refusedWith("A_VERY_LONG_REASON_THAT_NOBODY_WOULD_NAME_A_REASON_THIS_WAY"))
	if f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1")); f.Answer.Reason != "" {
		t.Errorf("%+v", f.Answer)
	}
}

func failing(code codes.Code, reason string) *fakeServer {
	return &fakeServer{probe: func() (*agentv1.ProbeResponse, error) {
		st := &spb.Status{Code: int32(code), Message: "jdbc: connection to db-secret-host refused"}
		if reason != "" {
			info, _ := anypb.New(&errdetails.ErrorInfo{Reason: reason, Domain: "sard.dev"})
			st.Details = []*anypb.Any{info}
		}
		return nil, status.ErrorProto(st)
	}}
}

func TestAnOlderServerAnswersUnimplemented(t *testing.T) {
	sard := newCA(t, "sard")
	w := startServer(t, sard, sard, failing(codes.Unimplemented, ""))
	if f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1")); f.Answer.Kind != hostcheck.AnswerOlder {
		t.Errorf("%+v", f.Answer)
	}
}

func TestAServerFaultIsReportedByCodeAndReasonNeverByItsText(t *testing.T) {
	sard := newCA(t, "sard")
	w := startServer(t, sard, sard, failing(codes.Unavailable, "INTERNAL_RETRYABLE"))
	f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1"))
	if f.Answer.Kind != hostcheck.AnswerFailed || f.Answer.Detail != "code = Unavailable (INTERNAL_RETRYABLE)" {
		t.Errorf("%+v", f.Answer)
	}
	w = startServer(t, sard, sard, failing(codes.Internal, ""))
	if f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1")); f.Answer.Detail != "code = Internal" {
		t.Errorf("%+v", f.Answer)
	}
}

func TestAServerThatDoesNotAnswerTheCallIsATimeout(t *testing.T) {
	sard := newCA(t, "sard")
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := &fakeServer{probe: func() (*agentv1.ProbeResponse, error) { <-release; return nil, errors.New("late") }}
	w := startServer(t, sard, sard, srv)
	req := w.request(t, sard, "127.0.0.1")
	req.Timeout = 200 * time.Millisecond
	if f := serverprobe.Run(context.Background(), req); f.Answer.Kind != hostcheck.AnswerTimeout {
		t.Errorf("%+v", f.Answer)
	}
}

func TestAnAnswerWithoutATimeIsAFailure(t *testing.T) {
	sard := newCA(t, "sard")
	srv := &fakeServer{probe: func() (*agentv1.ProbeResponse, error) { return &agentv1.ProbeResponse{}, nil }}
	w := startServer(t, sard, sard, srv)
	if f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1")); f.Answer.Kind != hostcheck.AnswerFailed {
		t.Errorf("%+v", f.Answer)
	}
}

func TestAServerCertificateOfAnotherCAFailsTheHandshake(t *testing.T) {
	sard, other := newCA(t, "sard"), newCA(t, "other")
	w := startServer(t, other, sard, okServer(epoch))
	w.sardCA = sard // the agent trusts sard only, the server shows the certificate of other
	req := w.request(t, sard, "127.0.0.1")
	f := serverprobe.Run(context.Background(), req)
	if f.TLS.Kind != hostcheck.FailUnknownCA || f.Answer.Kind != hostcheck.AnswerNone || w.server.probes.Load() != 0 {
		t.Errorf("%+v", f)
	}
}

func TestAServerCertificateForAnotherNameFailsTheHandshake(t *testing.T) {
	sard := newCA(t, "sard")
	w := startServer(t, sard, sard, okServer(epoch))
	f := serverprobe.Run(context.Background(), w.request(t, sard, "other.example.org"))
	if f.TLS.Kind != hostcheck.FailName {
		t.Errorf("%+v", f)
	}
}

func TestAServerThatDoesNotTrustTheCertificateOfTheAgentIsToldApartFromTheRest(t *testing.T) {
	sard, stranger := newCA(t, "sard"), newCA(t, "stranger")
	w := startServer(t, sard, sard, okServer(epoch))
	f := serverprobe.Run(context.Background(), w.request(t, stranger, "127.0.0.1")) // a certificate of another CA
	if f.TLS.Kind != hostcheck.FailClientCert || w.server.probes.Load() != 0 {
		t.Errorf("%+v", f)
	}
}

func TestAListenerThatNeverAnswersTheHandshakeIsATimeoutOfTheHandshake(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	sard := newCA(t, "sard")
	w := world{sardCA: sard, addr: l.Addr().String()}
	req := w.request(t, sard, "127.0.0.1")
	req.Timeout = 300 * time.Millisecond
	start := time.Now()
	f := serverprobe.Run(context.Background(), req)
	if f.Reach.Kind != hostcheck.FailNone || f.TLS.Kind != hostcheck.FailTimeout || time.Since(start) > 2*time.Second {
		t.Errorf("%+v after %v", f, time.Since(start))
	}
}

func TestAPortThatIsClosedIsRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	addr := l.Addr().String()
	_ = l.Close()
	sard := newCA(t, "sard")
	w := world{sardCA: sard, addr: addr}
	f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1"))
	if f.Reach.Kind != hostcheck.FailRefused || f.TLS.Kind != hostcheck.FailNone {
		t.Errorf("%+v", f)
	}
}

func TestFailuresToConnectAreToldApart(t *testing.T) {
	sard := newCA(t, "sard")
	cases := []struct {
		name string
		err  error
		want hostcheck.FailKind
		text string
	}{
		{"no such host", &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}, hostcheck.FailNoSuchHost, ""},
		{"timeout", context.DeadlineExceeded, hostcheck.FailTimeout, ""},
		{"refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, hostcheck.FailRefused, ""},
		{"other", errors.New("network is unreachable"), hostcheck.FailOther, "network is unreachable"},
	}
	for _, c := range cases {
		req := serverprobe.Request{Address: "x:1", TLS: &tls.Config{RootCAs: sard.pool}, Timeout: time.Second, Now: time.Now,
			Dial: func(context.Context, string, string) (net.Conn, error) { return nil, c.err }}
		f := serverprobe.Run(context.Background(), req)
		if f.Reach.Kind != c.want || f.Reach.Detail != c.text {
			t.Errorf("%s: %+v", c.name, f.Reach)
		}
	}
}

func TestAHandshakeThatFailsForAnotherReasonKeepsItsText(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("HTTP/1.1 400 not tls\r\n\r\n"))
			_ = c.Close()
		}
	}()
	sard := newCA(t, "sard")
	w := world{sardCA: sard, addr: l.Addr().String()}
	f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1"))
	if f.TLS.Kind != hostcheck.FailOther || f.TLS.Detail == "" {
		t.Errorf("%+v", f.TLS)
	}
}

func TestWithoutUsableTLSFilesTheHandshakeFailsWithTheReasonAndTheServerIsNotAsked(t *testing.T) {
	sard := newCA(t, "sard")
	srv := okServer(epoch)
	w := startServer(t, sard, sard, srv)
	req := w.request(t, sard, "127.0.0.1")
	req.TLS, req.TLSErr = nil, errors.New("tls.ca_file: no PEM certificates")
	f := serverprobe.Run(context.Background(), req)
	if f.Reach.Kind != hostcheck.FailNone || f.TLS.Kind != hostcheck.FailOther || f.TLS.Detail != "tls.ca_file: no PEM certificates" || srv.probes.Load() != 0 {
		t.Errorf("%+v", f)
	}
}

func TestTheReasonOfAnotherDomainIsNotTrusted(t *testing.T) {
	sard := newCA(t, "sard")
	w := startServer(t, sard, sard, refusedIn("example.com", "CERT_REVOKED"))
	f := serverprobe.Run(context.Background(), w.request(t, sard, "127.0.0.1"))
	if f.Answer.Kind != hostcheck.AnswerRefused || f.Answer.Reason != "" {
		t.Errorf("%+v", f.Answer)
	}
}
