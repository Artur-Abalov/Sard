// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/transport"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

func durationOf(d time.Duration) *durationpb.Duration { return durationpb.New(d) }

// clientVerified reports whether the agent presented a certificate the
// server's CA verified.
func clientVerified(ctx context.Context) bool {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return false
	}
	info, ok := p.AuthInfo.(credentials.TLSInfo)
	return ok && len(info.State.VerifiedChains) > 0
}

// rig is one agent transport against one fake server.
type rig struct {
	ca       *authority
	server   *fakeServer
	clock    *fakeClock
	commands *commands
	state    *state
	tr       *transport.Transport
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ca := newAuthority(t)
	return newRigWith(t, ca, ca)
}

// newRigWith: the agent trusts ca and holds a certificate from it; the
// server's certificate comes from serverCA.
func newRigWith(t *testing.T, ca, serverCA *authority) *rig {
	t.Helper()
	r := &rig{ca: ca, server: startServer(t, ca, serverCA), clock: newClock(), commands: newCommands(), state: &state{}}
	r.commands.state = r.state
	tr, err := transport.New(transport.Options{
		Address: r.server.addr,
		TLS:     ca.agentFiles(t, ca),
		Register: func(context.Context) *agentv1.RegisterRequest {
			return &agentv1.RegisterRequest{Hostname: "db1", ProtocolVersion: 1}
		},
		Commands: r.commands,
		State:    r.state,
		Clock:    r.clock,
		Rand:     func() float64 { return 1 }, // the backoff's upper bound
		LogQueue: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.tr = tr
	return r
}

// run runs the reconnecting loop in the background.
func (r *rig) run(ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- r.tr.Run(ctx) }()
	return done
}

// backoff returns the next reconnect delay, skipping heartbeat timers.
func (r *rig) backoff(t *testing.T, heartbeat time.Duration) time.Duration {
	t.Helper()
	for {
		if d := r.clock.waitTimer(t); d != heartbeat {
			return d
		}
	}
}

// connect runs one connection in the background; the returned channel
// yields its error.
func (r *rig) connect(ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() { done <- r.tr.Connect(ctx) }()
	return done
}

func wait(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("Connect did not return")
		return nil
	}
}

func TestConnectsOverMutualTLSAndRegistersFirst(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := r.connect(ctx)
	r.server.next(t).read(t)
	r.server.mu.Lock()
	regs, verified := r.server.registered, r.server.verified
	r.server.mu.Unlock()
	if len(regs) != 1 || regs[0].GetHostname() != "db1" || !slices.Equal(verified, []bool{true}) {
		t.Fatalf("registered = %v, client certificate verified = %v", regs, verified)
	}
	cancel()
	if err := wait(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// The agent trusts only tls.ca_file: a server with a certificate from
// another CA fails the handshake before anything is sent.
func TestRefusesAServerOfAnotherCA(t *testing.T) {
	r := newRigWith(t, newAuthority(t), newAuthority(t))
	err := wait(t, r.connect(context.Background()))
	if status.Code(err) != codes.Unavailable || !strings.Contains(err.Error(), "certificate signed by unknown authority") {
		t.Fatalf("err = %v", err)
	}
	if len(r.server.registered) != 0 || r.server.connectCount() != 0 {
		t.Fatalf("server got %d Register, %d Connect", len(r.server.registered), r.server.connectCount())
	}
}

func TestHelloIsTheFirstMessageWithTheRunningCommands(t *testing.T) {
	r := newRig(t)
	r.state.running = []string{"cmd-1", "cmd-2"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.connect(ctx)
	first := r.server.next(t).read(t)
	if got := first.GetHello().GetRunningCommandIds(); !slices.Equal(got, []string{"cmd-1", "cmd-2"}) {
		t.Fatalf("first message = %v", first)
	}
}

func TestServerMessagesReachTheExecutor(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.connect(ctx)
	ss := r.server.next(t)
	ss.read(t)
	ss.send <- &agentv1.ConnectResponse{Message: &agentv1.ConnectResponse_RunStep{RunStep: &agentv1.RunStep{CommandId: "c1"}}}
	ss.send <- &agentv1.ConnectResponse{Message: &agentv1.ConnectResponse_CancelStep{CancelStep: &agentv1.CancelStep{CommandId: "c1"}}}
	ss.send <- &agentv1.ConnectResponse{Message: &agentv1.ConnectResponse_ResultAck{ResultAck: &agentv1.ResultAck{CommandId: "c0"}}}
	ss.send <- &agentv1.ConnectResponse{} // an empty message is ignored
	for _, want := range []string{"submit c1", "cancel c1", "ack c0"} {
		if got := r.commands.event(t); got != want {
			t.Fatalf("executor got %q, want %q", got, want)
		}
	}
}

// Heartbeats follow the interval of RegisterResponse and carry the clock's time.
func TestHeartbeatFollowsTheRegisteredInterval(t *testing.T) {
	r := newRig(t)
	r.server.register = func() (*agentv1.RegisterResponse, error) { return registered("agent-1", 45*time.Second), nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.connect(ctx)
	ss := r.server.next(t)
	ss.read(t)
	for range 2 {
		if d := r.clock.waitTimer(t); d != 45*time.Second {
			t.Fatalf("heartbeat timer = %v, want 45s", d)
		}
		r.clock.Advance(45 * time.Second)
		msg := ss.read(t)
		if hb := msg.GetHeartbeat(); hb == nil || !hb.GetSentAt().AsTime().Equal(r.clock.Now()) {
			t.Fatalf("message = %v, want heartbeat at %v", msg, r.clock.Now())
		}
	}
}

// Any positive interval is the server's to choose, however short.
func TestTinyHeartbeatIntervalIsKept(t *testing.T) {
	r := newRig(t)
	r.server.register = func() (*agentv1.RegisterResponse, error) { return registered("a", time.Nanosecond), nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.connect(ctx)
	r.server.next(t).read(t)
	if d := r.clock.waitTimer(t); d != time.Nanosecond {
		t.Fatalf("heartbeat timer = %v", d)
	}
}

func TestMissingHeartbeatIntervalFallsBackToTheDefault(t *testing.T) {
	r := newRig(t)
	r.server.register = func() (*agentv1.RegisterResponse, error) { return &agentv1.RegisterResponse{AgentId: "a"}, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.connect(ctx)
	r.server.next(t).read(t)
	if d := r.clock.waitTimer(t); d != 30*time.Second {
		t.Fatalf("heartbeat timer = %v", d)
	}
}

// Register refusals that retrying cannot fix are typed and stop before Connect.
func TestRegisterRefusalsAreTyped(t *testing.T) {
	cases := map[codes.Code]error{
		codes.FailedPrecondition: transport.ErrIncompatibleProtocol,
		codes.Unauthenticated:    transport.ErrNotAuthorized,
		codes.PermissionDenied:   transport.ErrNotAuthorized,
	}
	for code, want := range cases {
		r := newRig(t)
		r.server.register = func() (*agentv1.RegisterResponse, error) { return nil, status.Error(code, "no") }
		err := wait(t, r.connect(context.Background()))
		if !errors.Is(err, want) || !transport.IsPermanent(err) || r.server.connectCount() != 0 {
			t.Errorf("%v: err = %v, connects = %d", code, err, r.server.connectCount())
		}
	}
}

func TestOtherRegisterFailuresAreNotPermanent(t *testing.T) {
	r := newRig(t)
	r.server.register = func() (*agentv1.RegisterResponse, error) { return nil, status.Error(codes.Unavailable, "restarting") }
	err := wait(t, r.connect(context.Background()))
	if err == nil || transport.IsPermanent(err) || !strings.HasPrefix(err.Error(), "register: ") {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectEndsWhenTheServerClosesTheStream(t *testing.T) {
	r := newRig(t)
	done := r.connect(context.Background())
	ss := r.server.next(t)
	ss.read(t)
	close(ss.done)
	err := wait(t, done)
	if err == nil || transport.IsPermanent(err) || !strings.HasPrefix(err.Error(), "stream: ") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewChecksTheTLSFiles(t *testing.T) {
	ca := newAuthority(t)
	good := ca.agentFiles(t, ca)
	notPEM := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(notPEM, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]transport.Options{
		"tls.ca_file is required":                                           {Address: "h:1", TLS: config.TLS{CertFile: good.CertFile, KeyFile: good.KeyFile}},
		"tls.cert_file is required":                                         {Address: "h:1", TLS: config.TLS{CAFile: good.CAFile, KeyFile: good.KeyFile}},
		"tls.key_file is required":                                          {Address: "h:1", TLS: config.TLS{CAFile: good.CAFile, CertFile: good.CertFile}},
		"tls.ca_file: no PEM certificates":                                  {Address: "h:1", TLS: config.TLS{CAFile: notPEM, CertFile: good.CertFile, KeyFile: good.KeyFile}},
		"server.address: address sard.example.com: missing port in address": {Address: "sard.example.com", TLS: good},
		"tls.ca_file: open /nonexistent/ca":                                 {Address: "h:1", TLS: config.TLS{CAFile: "/nonexistent/ca", CertFile: good.CertFile, KeyFile: good.KeyFile}},
	}
	for want, opts := range cases {
		_, err := transport.New(opts)
		if !errors.Is(err, transport.ErrInvalidOptions) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v", want, err)
		}
	}
}

// A broken client key fails the handshake on the agent's side; the error
// names the files, never their content.
func TestUnreadableClientCertificateFailsTheHandshake(t *testing.T) {
	r := newRig(t)
	files := r.ca.agentFiles(t, r.ca)
	if err := os.WriteFile(files.KeyFile, []byte("s3cr3t-not-a-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	tr, err := transport.New(transport.Options{Address: r.server.addr, TLS: files, Clock: r.clock, Commands: r.commands, State: r.state,
		Register: func(context.Context) *agentv1.RegisterRequest { return &agentv1.RegisterRequest{} }})
	if err != nil {
		t.Fatal(err)
	}
	err = tr.Connect(context.Background())
	if err == nil || !strings.Contains(err.Error(), files.KeyFile) || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("err = %v", err)
	}
	if len(r.server.registered) != 0 {
		t.Fatal("Register reached the server")
	}
}

func TestKeepaliveNoticesADeadConnection(t *testing.T) {
	if p := transport.KeepaliveForTest(); p.Time != 30*time.Second || p.Timeout != 10*time.Second || p.PermitWithoutStream {
		t.Fatalf("keepalive = %+v", p)
	}
}

func TestNewUsesTheRealClockByDefault(t *testing.T) {
	ca := newAuthority(t)
	tr, err := transport.New(transport.Options{Address: "localhost:1", TLS: ca.agentFiles(t, ca)})
	if err != nil || !tr.UsesRealClockForTest() {
		t.Fatalf("err = %v", err)
	}
}

// Cancelling while Register waits returns the context's error, not a
// register failure the caller would retry.
func TestCancelDuringRegister(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	r.server.register = func() (*agentv1.RegisterResponse, error) {
		cancel()
		return nil, status.Error(codes.Unavailable, "going away")
	}
	if err := wait(t, r.connect(ctx)); err != context.Canceled {
		t.Fatalf("err = %v", err)
	}
}

func TestUnusableAddressFailsToDial(t *testing.T) {
	ca := newAuthority(t)
	tr, err := transport.New(transport.Options{Address: "%zz:1", TLS: ca.agentFiles(t, ca)})
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Connect(context.Background()); err == nil || !strings.HasPrefix(err.Error(), "dial: ") {
		t.Fatalf("err = %v", err)
	}
}
