// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport

import (
	"context"
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"

	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// CallTimeout bounds one RenewCertificate or ConfirmCertificate call.
const CallTimeout = 30 * time.Second

// CertClient makes the two unary calls of a certificate renewal. Each call
// has a connection of its own that presents the pair it is given, so the
// stream of Connect is never touched and the confirmation is made by the
// new certificate alone (docs/specs/agent/agent-certificate-renewal.feature, П18).
type CertClient struct {
	address string
	caFile  string
}

// NewCertClient dials address (server.address) and trusts tls.ca_file.
func NewCertClient(address, caFile string) *CertClient {
	return &CertClient{address: address, caFile: caFile}
}

// Renew sends csrDER over a connection that presents the pair; it returns the
// new certificate chain as PEM.
func (c *CertClient) Renew(ctx context.Context, certFile, keyFile string, csrDER []byte) (string, error) {
	var chain string
	err := c.call(ctx, certFile, keyFile, func(ctx context.Context, client agentv1.AgentServiceClient) error {
		resp, err := client.RenewCertificate(ctx, &agentv1.RenewCertificateRequest{CsrDer: csrDER})
		chain = resp.GetCertificateChainPem()
		return err
	})
	return chain, err
}

// Confirm tells the server that the agent holds and uses the pair.
func (c *CertClient) Confirm(ctx context.Context, certFile, keyFile string) error {
	return c.call(ctx, certFile, keyFile, func(ctx context.Context, client agentv1.AgentServiceClient) error {
		_, err := client.ConfirmCertificate(ctx, &agentv1.ConfirmCertificateRequest{})
		return err
	})
}

func (c *CertClient) call(ctx context.Context, certFile, keyFile string, do func(context.Context, agentv1.AgentServiceClient) error) error {
	host, _, err := net.SplitHostPort(c.address)
	if err != nil {
		return fmt.Errorf("server.address: %w", err)
	}
	cfg, err := tlsConfig(config.TLS{CAFile: c.caFile, CertFile: certFile, KeyFile: keyFile}, host)
	if err != nil {
		return err
	}
	conn, err := grpc.NewClient(c.address, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	return callError(do(ctx, agentv1.NewAgentServiceClient(conn)))
}

// callError tells the renewal what the server answered, without gRPC: whether
// the answer is final (the server will say the same again, П20), whether the
// server lacks the call, and how long it asks to wait.
func callError(err error) error {
	st := grpcStatus(err)
	if st == nil {
		return err
	}
	return &certrenew.CallError{
		Text:        Describe(err),
		Final:       finalCode(st.Code()),
		Unsupported: st.Code() == codes.Unimplemented,
		RetryAfter:  RetryDelay(err),
		Err:         err,
	}
}

func finalCode(code codes.Code) bool {
	switch code {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.Unauthenticated, codes.PermissionDenied, codes.Unimplemented:
		return true
	}
	return false
}
