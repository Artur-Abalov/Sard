// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport_test

import (
	"context"
	"errors"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Artur-Abalov/sard/agent/internal/certrenew"
	"github.com/Artur-Abalov/sard/agent/internal/transport"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// docs/specs/agent/agent-certificate-renewal.feature: P18 (RenewCertificate and
// ConfirmCertificate over connections of their own).

func (s *fakeServer) RenewCertificate(ctx context.Context, req *agentv1.RenewCertificateRequest) (*agentv1.RenewCertificateResponse, error) {
	if s.renew == nil {
		return s.UnimplementedAgentServiceServer.RenewCertificate(ctx, req)
	}
	return s.renew(ctx, req)
}

func (s *fakeServer) ConfirmCertificate(ctx context.Context, req *agentv1.ConfirmCertificateRequest) (*agentv1.ConfirmCertificateResponse, error) {
	if s.confirm == nil {
		return s.UnimplementedAgentServiceServer.ConfirmCertificate(ctx, req)
	}
	return &agentv1.ConfirmCertificateResponse{}, s.confirm(ctx)
}

func TestRenewSendsTheCSROverTheGivenPairAndReturnsTheChain(t *testing.T) {
	r := newRig(t)
	files := r.ca.agentFiles(t, r.ca)
	var verified bool
	var csr []byte
	r.server.renew = func(ctx context.Context, req *agentv1.RenewCertificateRequest) (*agentv1.RenewCertificateResponse, error) {
		verified = clientVerified(ctx)
		csr = req.GetCsrDer()
		return &agentv1.RenewCertificateResponse{CertificateChainPem: "CHAIN"}, nil
	}
	chain, err := transport.NewCertClient(r.server.addr, files.CAFile).Renew(context.Background(), files.CertFile, files.KeyFile, []byte("csr"))
	if err != nil || chain != "CHAIN" || string(csr) != "csr" || !verified {
		t.Fatalf("chain = %q, err = %v, csr = %q, verified = %v", chain, err, csr, verified)
	}
}

func TestConfirmPresentsThePairItIsGiven(t *testing.T) {
	r := newRig(t)
	files := r.ca.agentFiles(t, r.ca)
	var verified bool
	r.server.confirm = func(ctx context.Context) error { verified = clientVerified(ctx); return nil }
	if err := transport.NewCertClient(r.server.addr, files.CAFile).Confirm(context.Background(), files.CertFile, files.KeyFile); err != nil || !verified {
		t.Fatalf("err = %v, verified = %v", err, verified)
	}
}

func TestACallThatTheServerRefusesReturnsTheStatus(t *testing.T) {
	r := newRig(t)
	files := r.ca.agentFiles(t, r.ca)
	r.server.confirm = func(context.Context) error {
		return reasoned(codes.Unauthenticated, "x", "sard.dev", "CERT_SUPERSEDED", nil)
	}
	client := transport.NewCertClient(r.server.addr, files.CAFile)
	err := client.Confirm(context.Background(), files.CertFile, files.KeyFile)
	if status.Code(err) != codes.Unauthenticated || transport.RefusalReason(err) != "CERT_SUPERSEDED" {
		t.Fatalf("err = %v", err)
	}
	if _, err := client.Renew(context.Background(), files.CertFile, files.KeyFile, nil); status.Code(err) != codes.Unimplemented {
		t.Fatalf("err = %v", err)
	}
}

func TestACallWithoutAUsablePairOrCAFails(t *testing.T) {
	r := newRig(t)
	files := r.ca.agentFiles(t, r.ca)
	missing := filepath.Join(t.TempDir(), "absent")
	client := transport.NewCertClient(r.server.addr, files.CAFile)
	if err := client.Confirm(context.Background(), files.CertFile, missing); err == nil {
		t.Error("a missing key was accepted")
	}
	if err := transport.NewCertClient(r.server.addr, missing).Confirm(context.Background(), files.CertFile, files.KeyFile); err == nil {
		t.Error("a missing CA was accepted")
	}
	if err := transport.NewCertClient("no-port", files.CAFile).Confirm(context.Background(), files.CertFile, files.KeyFile); err == nil {
		t.Error("an address without a port was accepted")
	}
	if _, err := client.Renew(context.Background(), files.CertFile, missing, nil); err == nil {
		t.Error("a missing key was accepted")
	}
}

// The answers of the server reach the renewal as certrenew.CallError.
func TestTheAnswersOfTheServerAreMadeIntoCallErrors(t *testing.T) {
	r := newRig(t)
	files := r.ca.agentFiles(t, r.ca)
	retry, err := status.New(codes.ResourceExhausted, "often").WithDetails(
		&errdetails.ErrorInfo{Domain: "sard.dev", Reason: "RENEWAL_TOO_FREQUENT"},
		&errdetails.RetryInfo{RetryDelay: durationOf(40 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		answer error
		want   certrenew.CallError
	}{
		{reasoned(codes.Unauthenticated, "x", "sard.dev", "CERT_SUPERSEDED", nil), certrenew.CallError{Text: "Unauthenticated CERT_SUPERSEDED", Final: true}},
		{status.Error(codes.InvalidArgument, "x"), certrenew.CallError{Text: "InvalidArgument", Final: true}},
		{status.Error(codes.FailedPrecondition, "x"), certrenew.CallError{Text: "FailedPrecondition", Final: true}},
		{status.Error(codes.PermissionDenied, "x"), certrenew.CallError{Text: "PermissionDenied", Final: true}},
		{status.Error(codes.Unimplemented, "x"), certrenew.CallError{Text: "Unimplemented", Final: true, Unsupported: true}},
		{status.Error(codes.Unavailable, "x"), certrenew.CallError{Text: "Unavailable"}},
		{status.Error(codes.Internal, "x"), certrenew.CallError{Text: "Internal"}},
		{retry.Err(), certrenew.CallError{Text: "ResourceExhausted RENEWAL_TOO_FREQUENT", RetryAfter: 40 * time.Minute}},
	}
	for _, row := range rows {
		r.server.confirm = func(context.Context) error { return row.answer }
		err := transport.NewCertClient(r.server.addr, files.CAFile).Confirm(context.Background(), files.CertFile, files.KeyFile)
		var got *certrenew.CallError
		if !errors.As(err, &got) || got.Text != row.want.Text || got.Final != row.want.Final || got.Unsupported != row.want.Unsupported || got.RetryAfter != row.want.RetryAfter {
			t.Errorf("%v: got %+v", row.answer, got)
		}
	}
}
