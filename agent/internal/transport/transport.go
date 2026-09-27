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
}

// Transport connects the agent to the server.
type Transport struct {
	opts  Options
	creds credentials.TransportCredentials
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
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	return &Transport{opts: opts, creds: credentials.NewTLS(cfg)}, nil
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

// serve runs one stream: Hello first, then heartbeats out and commands in.
// Only the heartbeat goroutine sends after Hello (gRPC forbids concurrent Send).
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
	errs := make(chan error, 2)
	go func() { errs <- t.receive(stream) }()
	go func() { errs <- t.heartbeat(ctx, stream, interval) }()
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
			// An ack for an unknown command is the executor's no-op.
			_ = t.opts.Commands.Ack(m.ResultAck.GetCommandId())
		}
	}
}

func (t *Transport) heartbeat(ctx context.Context, stream agentv1.AgentService_ConnectClient, interval time.Duration) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.opts.Clock.After(interval):
			hb := &agentv1.Heartbeat{SentAt: timestamppb.New(t.opts.Clock.Now())}
			if err := stream.Send(&agentv1.ConnectRequest{Message: &agentv1.ConnectRequest_Heartbeat{Heartbeat: hb}}); err != nil {
				return err
			}
		}
	}
}
