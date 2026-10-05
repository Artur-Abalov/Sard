// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package transport is the agent's side of AgentService: it dials out over
// mTLS (the agent never listens), registers, and keeps the Connect stream
// open. See docs/adr/0009-agent-transport-mtls.md and 0014-local-ca.md.
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/Artur-Abalov/sard/agent/internal/config"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// Enroller obtains the agent's certificate with a one-time token (A2).
type Enroller interface {
	Enroll(ctx context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error)
}

// Commands receives what the server sends; the executor (A4) implements it.
// Methods are called from the receive loop and must not block for long.
type Commands interface {
	Submit(step *agentv1.RunStep)
	Cancel(commandID string)
	Ack(commandID string) error
}

// State is what the executor reports when a stream opens.
type State interface {
	// RunningIDs are the commands queued or running, for Hello.
	RunningIDs() []string
	// PendingResults are the finished commands the server has not acked yet.
	PendingResults() []*agentv1.StepResult
}

// Clock is time for heartbeats and backoff; tests drive it by hand.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// DefaultHeartbeat applies when RegisterResponse carries no interval.
const DefaultHeartbeat = 30 * time.Second

// Reconnect backoff (answers of the owner, phase 1 of A3).
const (
	BackoffBase   = time.Second
	BackoffMax    = time.Minute
	HealthyStream = 30 * time.Second
)

// DefaultLogQueue is the default Options.LogQueue.
const DefaultLogQueue = 1024

// Client keepalive: a stream whose connection died silently is noticed
// within about Time+Timeout even when nothing is being sent.
const (
	keepaliveTime    = 30 * time.Second
	keepaliveTimeout = 10 * time.Second
)

var (
	// ErrInvalidOptions is returned by New for missing or unusable settings.
	ErrInvalidOptions = errors.New("invalid transport options")
	// ErrIncompatibleProtocol: Register answered FAILED_PRECONDITION.
	ErrIncompatibleProtocol = errors.New("server does not support this agent's protocol version")
	// ErrNotAuthorized: Register answered UNAUTHENTICATED or PERMISSION_DENIED,
	// e.g. a revoked or expired agent certificate.
	ErrNotAuthorized = errors.New("server refused the agent certificate")
)

// IsPermanent reports whether reconnecting cannot help.
func IsPermanent(err error) bool {
	return errors.Is(err, ErrIncompatibleProtocol) || errors.Is(err, ErrNotAuthorized)
}

// Options configure a Transport. Clock may be nil.
type Options struct {
	// Address is server.address, host:port; the host is checked against the
	// server certificate.
	Address string
	TLS     config.TLS
	// Register builds the RegisterRequest; it runs before every stream.
	Register func(ctx context.Context) *agentv1.RegisterRequest
	Commands Commands
	State    State
	Clock    Clock
	// Rand returns a number in [0, 1) for the backoff jitter; nil is math/rand.
	Rand func() float64
	// LogQueue is how many log lines may wait to be sent; zero is DefaultLogQueue.
	LogQueue int
	// Logger receives the life of the connection: streams opened and lost,
	// reconnects, Hello, results sent and acknowledged. Never a message's
	// content. Nil discards.
	Logger *slog.Logger
}

// Transport connects the agent to the server. Its Progress, Result and Log
// methods are the executor's way to report (the Sink of A4).
type Transport struct {
	opts  Options
	creds credentials.TransportCredentials
	out   *outbox
}

// New checks the options and loads the CA; the client certificate is read
// at every handshake, so a renewed one is picked up on the next connection.
func New(opts Options) (*Transport, error) {
	host, _, err := net.SplitHostPort(opts.Address)
	if err != nil {
		return nil, fmt.Errorf("%w: server.address: %w", ErrInvalidOptions, err)
	}
	cfg, err := tlsConfig(opts.TLS, host)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidOptions, err)
	}
	withDefaults(&opts)
	return &Transport{opts: opts, creds: credentials.NewTLS(cfg), out: newOutbox(opts.LogQueue)}, nil
}

func withDefaults(o *Options) {
	if o.Clock == nil {
		o.Clock = realClock{}
	}
	if o.Rand == nil {
		o.Rand = rand.Float64
	}
	if o.LogQueue <= 0 {
		o.LogQueue = DefaultLogQueue
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
}

// Progress reports how far a command got; only the latest report per
// command waits to be sent. It never blocks.
func (t *Transport) Progress(p *agentv1.StepProgress) { t.out.Progress(p) }

// Result reports a finished command. It is sent on every stream until the
// server acks it and is never dropped. It never blocks.
func (t *Transport) Result(r *agentv1.StepResult) { t.out.Result(r) }

// Log queues one line of a command's output. When LogQueue lines are
// waiting it blocks until there is room (back pressure), so it must not be
// called while holding a lock the transport's callbacks need. After Run
// returns, lines are dropped.
func (t *Transport) Log(commandID string, line *agentv1.LogLine) { t.out.Log(commandID, line) }

// Run connects and reconnects until ctx ends (nil) or the server refuses
// the agent for good (see IsPermanent). Between attempts it waits with
// exponential backoff and full jitter: up to BackoffBase·2ⁿ, capped at
// BackoffMax; a stream that lived HealthyStream resets the growth.
func (t *Transport) Run(ctx context.Context) error {
	defer t.out.close()
	attempt := 0
	for {
		started := t.opts.Clock.Now()
		err := t.Connect(ctx)
		if stop, err := t.stopReconnecting(ctx, err); stop {
			return err
		}
		if t.opts.Clock.Now().Sub(started) >= HealthyStream {
			attempt = 0
		}
		delay := t.backoff(attempt)
		t.opts.Logger.Info("reconnecting", "attempt", attempt+1, "delay", delay)
		if !t.sleep(ctx, delay) {
			return nil
		}
		attempt++
	}
}

// stopReconnecting: a stopped agent returns nil, a permanent refusal its
// error; any other end of a connection is logged and retried.
func (t *Transport) stopReconnecting(ctx context.Context, err error) (bool, error) {
	if ctx.Err() != nil {
		return true, nil
	}
	if IsPermanent(err) {
		t.opts.Logger.Error("the server refused the agent; not reconnecting", "error", err)
		return true, err
	}
	t.opts.Logger.Info("connection to the server lost", "code", status.Code(err).String(), "error", err)
	return false, nil
}

// sleep waits d; false when ctx ended first.
func (t *Transport) sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-t.opts.Clock.After(d):
		return true
	}
}

func (t *Transport) backoff(attempt int) time.Duration {
	limit := BackoffMax
	if attempt < 6 { // 1 s · 2⁶ already exceeds the cap
		limit = min(BackoffBase<<attempt, BackoffMax)
	}
	return time.Duration(t.opts.Rand() * float64(limit))
}

// tlsConfig trusts only the Sard CA and verifies the server's name.
func tlsConfig(files config.TLS, serverName string) (*tls.Config, error) {
	if err := requireFiles(files); err != nil {
		return nil, err
	}
	caPEM, err := os.ReadFile(files.CAFile)
	if err != nil {
		return nil, fmt.Errorf("tls.ca_file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("tls.ca_file: no PEM certificates in %q", files.CAFile)
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: serverName,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			pair, err := tls.LoadX509KeyPair(files.CertFile, files.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("tls.cert_file %q, tls.key_file %q: %w", files.CertFile, files.KeyFile, err)
			}
			return &pair, nil
		},
	}, nil
}

func requireFiles(files config.TLS) error {
	for _, f := range []struct{ key, path string }{{"tls.ca_file", files.CAFile}, {"tls.cert_file", files.CertFile}, {"tls.key_file", files.KeyFile}} {
		if f.path == "" {
			return fmt.Errorf("%s is required", f.key)
		}
	}
	return nil
}

// Connect makes one connection: Register, then the Connect stream until it
// ends. It returns ctx's error when ctx ends; a permanent error (see
// IsPermanent) when the server refuses the agent; any other error means
// the caller may retry.
func (t *Transport) Connect(ctx context.Context) error {
	conn, err := grpc.NewClient(t.opts.Address,
		grpc.WithTransportCredentials(t.creds),
		grpc.WithKeepaliveParams(keepaliveParams()),
	)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.Close() }()
	client := agentv1.NewAgentServiceClient(conn)
	resp, err := client.Register(ctx, t.opts.Register(ctx))
	if err != nil {
		return registerError(ctx, err)
	}
	return t.serve(ctx, client, heartbeatInterval(resp))
}

func keepaliveParams() keepalive.ClientParameters {
	return keepalive.ClientParameters{Time: keepaliveTime, Timeout: keepaliveTimeout}
}

func registerError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch status.Code(err) {
	case codes.FailedPrecondition:
		return fmt.Errorf("register: %w: %w", ErrIncompatibleProtocol, err)
	case codes.Unauthenticated, codes.PermissionDenied:
		return fmt.Errorf("register: %w: %w", ErrNotAuthorized, err)
	}
	return fmt.Errorf("register: %w", err)
}

func heartbeatInterval(resp *agentv1.RegisterResponse) time.Duration {
	if d := resp.GetHeartbeatInterval().AsDuration(); d > 0 {
		return d
	}
	return DefaultHeartbeat
}

// serve runs one stream: Hello first, then the unacked results, then
// whatever the executor reports and heartbeats; commands come in on a
// second goroutine. Only the sender goroutine calls Send after Hello (gRPC
// forbids concurrent Send).
func (t *Transport) serve(parent context.Context, client agentv1.AgentServiceClient, interval time.Duration) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		return streamError(parent, err)
	}
	hello := &agentv1.Hello{RunningCommandIds: t.opts.State.RunningIDs()}
	if err := stream.Send(&agentv1.ConnectRequest{Message: &agentv1.ConnectRequest_Hello{Hello: hello}}); err != nil {
		return streamError(parent, err)
	}
	pending := t.opts.State.PendingResults()
	t.opts.Logger.Info("connected to the server", "heartbeat", interval)
	t.opts.Logger.Info("hello sent", "running", len(hello.GetRunningCommandIds()), "pending_results", len(pending))
	t.out.startStream(pending)
	errs := make(chan error, 2)
	go func() { errs <- t.receive(stream) }()
	go func() { errs <- t.send(ctx, stream, interval) }()
	err = <-errs
	cancel()
	<-errs
	return streamError(parent, err)
}

func streamError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("stream: %w", err)
}

// receive hands server messages to the executor until the stream ends.
func (t *Transport) receive(stream agentv1.AgentService_ConnectClient) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}
		switch m := msg.GetMessage().(type) {
		case *agentv1.ConnectResponse_RunStep:
			t.opts.Commands.Submit(m.RunStep)
		case *agentv1.ConnectResponse_CancelStep:
			t.opts.Commands.Cancel(m.CancelStep.GetCommandId())
		case *agentv1.ConnectResponse_ResultAck:
			t.opts.Logger.Info("result acknowledged", "command_id", m.ResultAck.GetCommandId())
			t.out.ack(m.ResultAck.GetCommandId())
			// An ack for an unknown command is the executor's no-op.
			_ = t.opts.Commands.Ack(m.ResultAck.GetCommandId())
		}
	}
}

// send is the stream's only sender: queued messages as soon as they are
// there, and a heartbeat every interval even while messages keep coming.
func (t *Transport) send(ctx context.Context, stream agentv1.AgentService_ConnectClient, interval time.Duration) error {
	s := &sender{t: t, stream: stream, interval: interval, beat: t.opts.Clock.After(interval)}
	for {
		if err := s.step(ctx); err != nil {
			return err
		}
	}
}

// step sends a due heartbeat, then one queued message, or waits for work.
func (s *sender) step(ctx context.Context) error {
	if err := s.beatIfDue(); err != nil {
		return err
	}
	sent, err := s.sendNext()
	if err != nil || sent {
		return err
	}
	return s.wait(ctx)
}

type sender struct {
	t        *Transport
	stream   agentv1.AgentService_ConnectClient
	interval time.Duration
	beat     <-chan time.Time
}

func (s *sender) beatIfDue() error {
	select {
	case <-s.beat:
		return s.heartbeat()
	default:
		return nil
	}
}

// sendNext sends one queued message; a failed log chunk goes back in the queue.
func (s *sender) sendNext() (bool, error) {
	msg, ok := s.t.out.next()
	if !ok {
		return false, nil
	}
	if err := s.stream.Send(msg); err != nil {
		s.t.out.requeue(msg)
		return true, err
	}
	if r := msg.GetStepResult(); r != nil {
		s.t.opts.Logger.Info("result sent", "command_id", r.GetCommandId(), "status", r.GetStatus().String())
	}
	return true, nil
}

// wait blocks until there is something to send, a heartbeat is due or ctx ends.
func (s *sender) wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.t.out.ready():
		return nil
	case <-s.beat:
		return s.heartbeat()
	}
}

func (s *sender) heartbeat() error {
	s.beat = s.t.opts.Clock.After(s.interval)
	hb := &agentv1.Heartbeat{SentAt: timestamppb.New(s.t.opts.Clock.Now())}
	return s.stream.Send(&agentv1.ConnectRequest{Message: &agentv1.ConnectRequest_Heartbeat{Heartbeat: hb}})
}
