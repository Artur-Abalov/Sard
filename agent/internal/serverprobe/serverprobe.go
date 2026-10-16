// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package serverprobe makes the three steps of the checks of the server of
// sard-agent check (A8d-3, Ч14-Ч16): a TCP connection, an mTLS handshake and
// the read-only AgentService.Probe call. It collects facts; what they mean is
// internal/hostcheck. It never calls Register or Connect, so it cannot
// change the registration or the steps of the service (ADR 0038).
package serverprobe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/Artur-Abalov/sard/agent/internal/hostcheck"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// sardDomain is the google.rpc.ErrorInfo domain of the reasons of the
// server (ADR 0025).
const sardDomain = "sard.dev"

// alertWait is how long to wait for the alert of a server that rejects the
// certificate of the agent after the handshake.
const alertWait = 500 * time.Millisecond

// maxReason bounds a reason of the server that is shown.
const maxReason = 40

// DialFunc opens a TCP connection.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Request is what Run needs. TLS is the configuration of the agent: the CA
// of Sard, the server name and the certificate of the agent.
type Request struct {
	Address string
	TLS     *tls.Config
	// TLSErr is why TLS could not be built from the files of the agent; the
	// address is still tried, but no handshake is made.
	TLSErr error
	// Timeout limits each access to the server (--connect-timeout).
	Timeout time.Duration
	// Now is the clock of the host.
	Now func() time.Time
	// Dial is nil for the network.
	Dial DialFunc
}

func (r Request) dial() DialFunc {
	if r.Dial != nil {
		return r.Dial
	}
	return (&net.Dialer{}).DialContext
}

// Run makes the steps in order and stops at the first that fails.
func Run(ctx context.Context, r Request) hostcheck.ServerFacts {
	f := hostcheck.ServerFacts{Address: r.Address, Timeout: r.Timeout}
	if f.Reach = reach(ctx, r); f.Reach.Kind != hostcheck.FailNone {
		return f
	}
	if r.TLSErr != nil {
		f.TLS = hostcheck.Failure{Kind: hostcheck.FailOther, Detail: r.TLSErr.Error()}
		return f
	}
	if f.TLS = handshake(ctx, r); f.TLS.Kind != hostcheck.FailNone {
		return f
	}
	probe(ctx, r, &f)
	return f
}

func reach(ctx context.Context, r Request) hostcheck.Failure {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	conn, err := r.dial()(ctx, "tcp", r.Address)
	if err != nil {
		return dialFailure(err)
	}
	_ = conn.Close()
	return hostcheck.Failure{}
}

func dialFailure(err error) hostcheck.Failure {
	var dns *net.DNSError
	switch {
	case errors.As(err, &dns) && dns.IsNotFound:
		return hostcheck.Failure{Kind: hostcheck.FailNoSuchHost}
	case errors.Is(err, syscall.ECONNREFUSED):
		return hostcheck.Failure{Kind: hostcheck.FailRefused}
	case isTimeout(err):
		return hostcheck.Failure{Kind: hostcheck.FailTimeout}
	}
	return hostcheck.Failure{Kind: hostcheck.FailOther, Detail: err.Error()}
}

func isTimeout(err error) bool {
	var nerr net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &nerr) && nerr.Timeout())
}

func handshake(ctx context.Context, r Request) hostcheck.Failure {
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	raw, err := r.dial()(ctx, "tcp", r.Address)
	if err != nil {
		return dialFailure(err)
	}
	cfg := r.TLS.Clone()
	cfg.NextProtos = []string{"h2"}
	conn := tls.Client(raw, cfg)
	defer func() { _ = conn.Close() }()
	if err := conn.HandshakeContext(ctx); err != nil {
		return handshakeFailure(err)
	}
	return alertAfterHandshake(conn, r.Timeout)
}

// alertAfterHandshake: with TLS 1.3 the handshake ends for the client before
// the server has judged its certificate; a rejection comes as the alert that
// the first read returns. A server that has nothing to say (it waits for the
// client) is not a failure.
func alertAfterHandshake(conn net.Conn, limit time.Duration) hostcheck.Failure {
	_ = conn.SetReadDeadline(time.Now().Add(min(limit, alertWait)))
	if _, err := conn.Read(make([]byte, 1)); err != nil && rejectsClient(err.Error()) {
		return hostcheck.Failure{Kind: hostcheck.FailClientCert}
	}
	return hostcheck.Failure{}
}

func handshakeFailure(err error) hostcheck.Failure {
	if kind, ok := certificateFailure(err); ok {
		return hostcheck.Failure{Kind: kind}
	}
	if isTimeout(err) {
		return hostcheck.Failure{Kind: hostcheck.FailTimeout}
	}
	return hostcheck.Failure{Kind: hostcheck.FailOther, Detail: err.Error()}
}

// certificateFailure: the handshake failed because of a certificate.
func certificateFailure(err error) (hostcheck.FailKind, bool) {
	var unknown x509.UnknownAuthorityError
	var name x509.HostnameError
	switch {
	case errors.As(err, &unknown):
		return hostcheck.FailUnknownCA, true
	case errors.As(err, &name):
		return hostcheck.FailName, true
	case rejectsClient(err.Error()):
		return hostcheck.FailClientCert, true
	}
	return hostcheck.FailNone, false
}

// rejectsClient: the alert of the server about the certificate of the agent.
// With TLS 1.3 it arrives after the handshake, in the first call.
func rejectsClient(text string) bool {
	return strings.Contains(text, "remote error: tls:") &&
		(strings.Contains(text, "certificate") || strings.Contains(text, "unknown ca"))
}

func probe(ctx context.Context, r Request, f *hostcheck.ServerFacts) {
	conn, err := grpc.NewClient(r.Address, r.dialOptions()...)
	if err != nil {
		f.Answer = hostcheck.Answer{Kind: hostcheck.AnswerFailed, Detail: err.Error()}
		return
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	f.Sent = r.Now()
	start := time.Now()
	resp, err := agentv1.NewAgentServiceClient(conn).Probe(ctx, &agentv1.ProbeRequest{})
	f.RTT = time.Since(start)
	if err != nil {
		answerOf(err, f)
		return
	}
	f.Answer = timeOf(resp, f)
}

func (r Request) dialOptions() []grpc.DialOption {
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(r.TLS))}
	if r.Dial == nil {
		return opts
	}
	return append(opts, grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
		return r.Dial(ctx, "tcp", address)
	}))
}

// timeOf takes the time of the server from the answer.
func timeOf(resp *agentv1.ProbeResponse, f *hostcheck.ServerFacts) hostcheck.Answer {
	if resp.GetServerTime().CheckValid() != nil {
		return hostcheck.Answer{Kind: hostcheck.AnswerFailed, Detail: "the answer has no valid time"}
	}
	f.ServerTime = resp.GetServerTime().AsTime()
	return hostcheck.Answer{Kind: hostcheck.AnswerAccepted}
}

// answerOf turns the error of Probe into the answer (or the failure of the
// handshake that only the first call showed).
func answerOf(err error, f *hostcheck.ServerFacts) {
	st, _ := status.FromError(err)
	switch st.Code() {
	case codes.Unauthenticated:
		f.Answer = hostcheck.Answer{Kind: hostcheck.AnswerRefused, Reason: reasonOf(st)}
	case codes.Unimplemented:
		f.Answer = hostcheck.Answer{Kind: hostcheck.AnswerOlder}
	case codes.DeadlineExceeded:
		f.Answer = hostcheck.Answer{Kind: hostcheck.AnswerTimeout}
	default:
		if rejectsClient(st.Message()) {
			f.TLS = hostcheck.Failure{Kind: hostcheck.FailClientCert}
			return
		}
		f.Answer = hostcheck.Answer{Kind: hostcheck.AnswerFailed, Detail: faultOf(st)}
	}
}

// faultOf names a failure of the server by its code and reason, never by its text.
func faultOf(st *status.Status) string {
	detail := "code = " + st.Code().String()
	if reason := reasonOf(st); reason != "" {
		detail += " (" + reason + ")"
	}
	return detail
}

// reasonOf is the reason of the ErrorInfo of Sard, if it is plain upper-case
// words: the text of the server is never copied into the output as it is.
func reasonOf(st *status.Status) string {
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == sardDomain {
			return plain(info.GetReason())
		}
	}
	return ""
}

func plain(reason string) string {
	if len(reason) > maxReason || strings.Trim(reason, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_0123456789") != "" {
		return ""
	}
	return reason
}
