// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package enroll

import (
	"context"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

// sardErrorDomain is the google.rpc.ErrorInfo domain the server uses
// (docs/adr/0025-grpc-error-model.md).
const sardErrorDomain = "sard.dev"

// tokenRefusedReasons: TOKEN_UNKNOWN, TOKEN_USED, TOKEN_EXPIRED and
// TOKEN_REVOKED all mean "get a new token"; none is worth retrying.
var tokenRefusedReasons = map[string]bool{
	"TOKEN_UNKNOWN": true,
	"TOKEN_USED":    true,
	"TOKEN_EXPIRED": true,
	"TOKEN_REVOKED": true,
}

// agentErrorReasons: the request itself was invalid; retrying verbatim
// cannot help, only an updated agent can (В4).
var agentErrorReasons = map[string]bool{
	"CSR_INVALID":      true,
	"HOSTNAME_INVALID": true,
}

// EnrollResult is the identity EnrollmentService.Enroll hands back.
type EnrollResult struct {
	AgentID             string
	CertificateChainPEM string
	CABundlePEM         string
}

// CallEnroll sends req over conn — which DialTOFU has already trust-checked
// — and classifies any failure by the reason string in the server's
// google.rpc.ErrorInfo (docs/adr/0025-grpc-error-model.md). This is
// the single point that turns a server response into an *Error; A2b reads
// only Class and Reason to build its message and pick an exit code. It
// does not retry: the server's contract promises only UNAVAILABLE is worth
// retrying, and retrying is A2b's decision, not this package's.
func CallEnroll(ctx context.Context, conn *grpc.ClientConn, req *agentv1.EnrollRequest) (*EnrollResult, error) {
	resp, err := agentv1.NewEnrollmentServiceClient(conn).Enroll(ctx, req)
	if err != nil {
		return nil, classifyEnrollError(err)
	}
	if resp.GetAgentId() == "" || resp.GetCertificateChainPem() == "" || resp.GetCaBundlePem() == "" {
		return nil, &Error{Class: ClassAgentError, Code: codes.OK.String(), msg: "server reported success without issuing a certificate"}
	}
	return &EnrollResult{
		AgentID:             resp.GetAgentId(),
		CertificateChainPEM: resp.GetCertificateChainPem(),
		CABundlePEM:         resp.GetCaBundlePem(),
	}, nil
}

// classifyEnrollError: once client.Enroll has been called, the request is
// on the wire; any failure that is not a definitive, reasoned response from
// the server (deadline, cancellation, a dropped connection) means the
// token's fate is unknown (В14).
func classifyEnrollError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return &Error{Class: ClassTemporary, TokenMaybeSpent: true, msg: "enroll: transport error", err: err}
	}
	reason, hasReason := errorInfoReason(st)
	if !hasReason {
		return classifyBareStatus(st)
	}
	return classifyReason(st.Code(), reason)
}

func errorInfoReason(st *status.Status) (string, bool) {
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == sardErrorDomain {
			return info.GetReason(), true
		}
	}
	return "", false
}

// classifyBareStatus handles a gRPC failure that carries no sard.dev
// ErrorInfo: a deadline, a cancellation or the connection dropping
// (Unavailable) is temporary (В14) and the request was already sent, so
// the token's fate is unknown; anything else is an unforeseen response
// (В4's last row).
func classifyBareStatus(st *status.Status) error {
	if isBareTemporaryCode(st.Code()) {
		return &Error{Class: ClassTemporary, TokenMaybeSpent: true, msg: "enroll: " + st.Code().String()}
	}
	return &Error{Class: ClassAgentError, Code: st.Code().String(), msg: "unforeseen server response: " + st.Message()}
}

func isBareTemporaryCode(c codes.Code) bool {
	return c == codes.DeadlineExceeded || c == codes.Canceled || c == codes.Unavailable
}

func classifyReason(code codes.Code, reason string) error {
	switch {
	case tokenRefusedReasons[reason]:
		return &Error{Class: ClassTokenRefused, Reason: reason}
	case reason == "TOKEN_FOREIGN_CA":
		return &Error{Class: ClassTrust, Reason: reason}
	case reason == "TOKEN_MALFORMED":
		return &Error{Class: ClassUsage, Reason: reason}
	case agentErrorReasons[reason]:
		return &Error{Class: ClassAgentError, Reason: reason, msg: "the token is intact; retry after updating the agent"}
	case reason == "INTERNAL_RETRYABLE":
		return &Error{Class: ClassTemporary, Reason: reason, msg: "temporary server problem; the token is intact"}
	default:
		return &Error{Class: ClassAgentError, Reason: reason, Code: code.String(), msg: "unforeseen reason from the server"}
	}
}
