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

// dialClassifyServerManual is dialClassifyServer, except the caller stops
// the server by hand instead of on t.Cleanup — needed to drop the
// connection mid-request.
func dialClassifyServerManual(t *testing.T, s *classifyServer) (*grpc.ClientConn, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	agentv1.RegisterEnrollmentServiceServer(srv, s)
	go func() { _ = srv.Serve(lis) }()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, srv.Stop
}

// F1: a connection dropped after the request was sent must not be confused
// with an unforeseen server response (В14) — the token's fate is unknown,
// same as a timeout, not "an updated agent might fix this".
func TestCallEnrollClassifiesAConnectionDropAfterSendingAsTemporaryWithTokenMaybeSpent(t *testing.T) {
	started := make(chan struct{})
	s := &classifyServer{answer: func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
		close(started)
		select {} // never respond; the server is stopped from outside instead
	}}
	conn, stop := dialClassifyServerManual(t, s)
	errCh := make(chan error, 1)
	go func() {
		_, err := enroll.CallEnroll(context.Background(), conn, &agentv1.EnrollRequest{})
		errCh <- err
	}()
	<-started
	stop() // abruptly closes the listener and every connection

	var err error
	select {
	case err = <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("CallEnroll did not return after the server stopped")
	}
	var eerr *enroll.Error
	if !errors.As(err, &eerr) {
		t.Fatalf("error type = %T, want *enroll.Error", err)
	}
	if eerr.Class != enroll.ClassTemporary {
		t.Fatalf("class = %q, want temporary", eerr.Class)
	}
	if !eerr.TokenMaybeSpent {
		t.Fatal("want TokenMaybeSpent after a connection drop following the request")
	}
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

// CallEnroll must reject a response that is missing exactly one of the
// three identity fields, even when the other two are present — a response
// missing only AgentId (say) is just as useless as one missing everything.
func TestCallEnrollTreatsAResponseMissingExactlyOneIdentityFieldAsAgentError(t *testing.T) {
	cases := map[string]func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error){
		"missing AgentId only": func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
			return &agentv1.EnrollResponse{AgentId: "", CertificateChainPem: "cert", CaBundlePem: "ca"}, nil
		},
		"missing CertificateChainPem only": func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
			return &agentv1.EnrollResponse{AgentId: "a1", CertificateChainPem: "", CaBundlePem: "ca"}, nil
		},
		"missing CaBundlePem only": func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
			return &agentv1.EnrollResponse{AgentId: "a1", CertificateChainPem: "cert", CaBundlePem: ""}, nil
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

// errorInfoReason must only recognize ErrorInfo from the server's own
// domain (sard.dev); a differently-domained ErrorInfo carrying a
// coincidentally matching reason string must not be read as if it were the
// server's own classification (F1's connection-drop fix depends on
// classifyBareStatus running instead in that case).
func TestCallEnrollIgnoresErrorInfoFromAForeignDomain(t *testing.T) {
	s := &classifyServer{answer: func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
		st, err := status.New(codes.Unavailable, "enroll refused").WithDetails(&errdetails.ErrorInfo{Reason: "TOKEN_USED", Domain: "not-sard.example"})
		if err != nil {
			panic(err)
		}
		return nil, st.Err()
	}}
	conn := dialClassifyServer(t, s)
	_, err := enroll.CallEnroll(context.Background(), conn, &agentv1.EnrollRequest{})
	var eerr *enroll.Error
	if !errors.As(err, &eerr) {
		t.Fatalf("error type = %T, want *enroll.Error", err)
	}
	// A foreign-domain ErrorInfo must be ignored entirely: classifyBareStatus
	// reads the bare Unavailable code as temporary, not classifyReason
	// reading "TOKEN_USED" as a token refusal.
	if eerr.Class != enroll.ClassTemporary {
		t.Fatalf("class = %q, want temporary (foreign-domain ErrorInfo must be ignored)", eerr.Class)
	}
	if eerr.Reason == "TOKEN_USED" {
		t.Fatal("a foreign-domain ErrorInfo's reason must never be read as the server's own classification")
	}
}

// isBareTemporaryCode treats a bare (no ErrorInfo) Canceled the same as
// DeadlineExceeded and Unavailable: all three mean the request's outcome
// is unknown, not an unforeseen server response. TestCallEnrollClassifiesAConnectionDropAfterSendingAsTemporaryWithTokenMaybeSpent
// exercises this indirectly (a dropped connection can surface as either
// Canceled or Unavailable depending on timing) but does not reliably pin
// down Canceled specifically; this test does, deterministically.
func TestCallEnrollClassifiesABareCanceledStatusAsTemporary(t *testing.T) {
	s := &classifyServer{answer: func(*agentv1.EnrollRequest) (*agentv1.EnrollResponse, error) {
		return nil, status.Error(codes.Canceled, "canceled")
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
	if !eerr.TokenMaybeSpent {
		t.Fatal("want TokenMaybeSpent for a bare Canceled status")
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
