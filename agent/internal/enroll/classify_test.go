// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// classifyServer answers Enroll however the test configures it and counts calls.
type classifyServer struct {
	agentv1.UnimplementedEnrollmentServiceServer
	calls  atomic.Int32
	answer func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error)
}

func (s *classifyServer) Enroll(_ context.Context, req *agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
	s.calls.Add(1)
	return s.answer(req)
}

func dialClassifyServer(t *testing.T, s *classifyServer) *grpc.ClientConn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	agentv1.RegisterEnrollmentServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func reasonRefusal(code codes.Code, reason string) error {
	st, err := status.New(code, "enroll refused").WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: "sard.dev"})
	if err != nil {
		panic(err)
	}
	return st.Err()
}

func TestCallEnrollClassifiesTokenRefusalsByReason(t *testing.T) {
	cases := []string{"TOKEN_UNKNOWN", "TOKEN_USED", "TOKEN_EXPIRED", "TOKEN_REVOKED"}
	for _, reason := range cases {
		t.Run(reason, func(t *testing.T) {
			s := &classifyServer{answer: func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
				return nil, reasonRefusal(codes.Unauthenticated, reason)
			}}
			conn := dialClassifyServer(t, s)
			_, err := enroll.CallEnroll(context.Background(), conn, &agentv1.EnrollRequest{})
			var eerr *enroll.Error
			if !errors.As(err, &eerr) {
				t.Fatalf("error type = %T, want *enroll.Error", err)
			}
			if eerr.Class != enroll.ClassTokenRefused {
				t.Fatalf("class = %q, want token-refused", eerr.Class)
			}
			if eerr.Reason != reason {
				t.Fatalf("reason = %q, want %q", eerr.Reason, reason)
			}
		})
	}
}

func TestCallEnrollClassifiesTokenForeignCAAsTrust(t *testing.T) {
	s := &classifyServer{answer: func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
		return nil, reasonRefusal(codes.Unauthenticated, "TOKEN_FOREIGN_CA")
	}}
	conn := dialClassifyServer(t, s)
	_, err := enroll.CallEnroll(context.Background(), conn, &agentv1.EnrollRequest{})
	requireClass(t, err, enroll.ClassTrust, "TOKEN_FOREIGN_CA")
}

func TestCallEnrollClassifiesTokenMalformedAsUsage(t *testing.T) {
	s := &classifyServer{answer: func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
		return nil, reasonRefusal(codes.InvalidArgument, "TOKEN_MALFORMED")
	}}
	conn := dialClassifyServer(t, s)
	_, err := enroll.CallEnroll(context.Background(), conn, &agentv1.EnrollRequest{})
	requireClass(t, err, enroll.ClassUsage, "TOKEN_MALFORMED")
}

func TestCallEnrollClassifiesAgentFaultReasons(t *testing.T) {
	for _, reason := range []string{"CSR_INVALID", "HOSTNAME_INVALID"} {
		t.Run(reason, func(t *testing.T) {
			s := &classifyServer{answer: func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
				return nil, reasonRefusal(codes.InvalidArgument, reason)
			}}
			conn := dialClassifyServer(t, s)
			_, err := enroll.CallEnroll(context.Background(), conn, &agentv1.EnrollRequest{})
			requireClass(t, err, enroll.ClassAgentError, reason)
		})
	}
}

func TestCallEnrollClassifiesInternalRetryableAsTemporaryWithoutSpendingTheToken(t *testing.T) {
	s := &classifyServer{answer: func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
		return nil, reasonRefusal(codes.Unavailable, "INTERNAL_RETRYABLE")
	}}
	conn := dialClassifyServer(t, s)
	_, err := enroll.CallEnroll(context.Background(), conn, &agentv1.EnrollRequest{})
	var eerr *enroll.Error
	if !errors.As(err, &eerr) {
		t.Fatalf("error type = %T, want *enroll.Error", err)
	}
	if eerr.Class != enroll.ClassTemporary {
		t.Fatalf("class = %q, want temporary", eerr.Class)
	}
	if eerr.TokenMaybeSpent {
		t.Fatal("a definitive INTERNAL_RETRYABLE response means the token is intact, not maybe spent")
	}
	if s.calls.Load() != 1 {
		t.Fatalf("calls = %d, want exactly 1: CallEnroll must not retry", s.calls.Load())
	}
}

func TestCallEnrollClassifiesUnforeseenResponsesAsAgentError(t *testing.T) {
	cases := map[string]func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error){
		"UNIMPLEMENTED without ErrorInfo": func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
			return nil, status.Error(codes.Unimplemented, "not implemented")
		},
		"INTERNAL without ErrorInfo": func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
			return nil, status.Error(codes.Internal, "boom")
		},
		"UNAUTHENTICATED with an unknown reason": func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
			return nil, reasonRefusal(codes.Unauthenticated, "SOMETHING_NEW")
		},
		"success without a certificate": func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
			return &agentv1.EnrollResponse{AgentId: "a1"}, nil
		},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			s := &classifyServer{answer: answer}
			conn := dialClassifyServer(t, s)
			_, err := enroll.CallEnroll(context.Background(), conn, &agentv1.EnrollRequest{})
			var eerr *enroll.Error
			if !errors.As(err, &eerr) {
				t.Fatalf("error type = %T, want *enroll.Error", err)
			}
			if eerr.Class != enroll.ClassAgentError {
				t.Fatalf("class = %q, want agent-error", eerr.Class)
			}
		})
	}
}

func TestCallEnrollReturnsTheIssuedIdentityOnSuccess(t *testing.T) {
	s := &classifyServer{answer: func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
		return &agentv1.EnrollResponse{AgentId: "a1", CertificateChainPem: "cert", CaBundlePem: "ca"}, nil
	}}
	conn := dialClassifyServer(t, s)
	res, err := enroll.CallEnroll(context.Background(), conn, &agentv1.EnrollRequest{})
	if err != nil {
		t.Fatalf("CallEnroll: %v", err)
	}
	if res.AgentID != "a1" || res.CertificateChainPEM != "cert" || res.CABundlePEM != "ca" {
		t.Fatalf("result = %+v", res)
	}
}

func TestCallEnrollTimeoutAfterTheRequestWasSentMarksTheTokenMaybeSpent(t *testing.T) {
	started := make(chan struct{})
	s := &classifyServer{answer: func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
		close(started)
		select {} // never respond
	}}
	conn := dialClassifyServer(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := enroll.CallEnroll(ctx, conn, &agentv1.EnrollRequest{})
	<-started
	var eerr *enroll.Error
	if !errors.As(err, &eerr) {
		t.Fatalf("error type = %T, want *enroll.Error", err)
	}
	if eerr.Class != enroll.ClassTemporary {
		t.Fatalf("class = %q, want temporary", eerr.Class)
	}
	if !eerr.TokenMaybeSpent {
		t.Fatal("want TokenMaybeSpent after a timeout following the request")
	}
}

func requireClass(t *testing.T, err error, class enroll.Class, reason string) {
	t.Helper()
	var eerr *enroll.Error
	if !errors.As(err, &eerr) {
		t.Fatalf("error type = %T, want *enroll.Error", err)
	}
	if eerr.Class != class {
		t.Fatalf("class = %q, want %q", eerr.Class, class)
	}
	if eerr.Reason != reason {
		t.Fatalf("reason = %q, want %q", eerr.Reason, reason)
	}
}
