// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

// sardErrorDomain is the google.rpc.ErrorInfo domain of the server's
// reasons (docs/adr/0025-grpc-error-model.md).
const sardErrorDomain = "sard.dev"

// RefusalSuffix ends every line that reports a permanent refusal.
const RefusalSuffix = "not reconnecting: fix the cause, then restart the agent"

// RefusalDetail explains a permanent refusal (see IsPermanent) to the
// operator: the status code and text, and the server's reason and metadata
// when it gave one. Control characters are escaped, so the result is one
// line; the agent ends it with RefusalSuffix.
func RefusalDetail(err error) string {
	parts := []string{statusText(err)}
	if info := sardErrorInfo(err); info != nil {
		parts = append(parts, "reason "+escape(info.GetReason()))
		parts = append(parts, metadata(info.GetMetadata())...)
	}
	return strings.Join(parts, ", ")
}

// RefusalReason is the server's reason of err (the ErrorInfo of domain
// sard.dev), or "".
func RefusalReason(err error) string {
	if info := sardErrorInfo(err); info != nil {
		return info.GetReason()
	}
	return ""
}

// Describe is err in one line without control characters: the gRPC code
// name and the server's reason, e.g. "Unavailable INTERNAL_RETRYABLE"; the
// text of any other error. It never includes the status message, which may
// echo what the agent sent.
func Describe(err error) string {
	st := grpcStatus(err)
	if st == nil {
		return escape(err.Error())
	}
	if reason := RefusalReason(err); reason != "" {
		return st.Code().String() + " " + escape(reason)
	}
	return st.Code().String()
}

// RetryDelay is the delay the server asks for in a RetryInfo, or 0.
func RetryDelay(err error) time.Duration {
	st := grpcStatus(err)
	if st == nil {
		return 0
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.RetryInfo); ok {
			return info.GetRetryDelay().AsDuration()
		}
	}
	return 0
}

func grpcStatus(err error) *status.Status {
	var se interface{ GRPCStatus() *status.Status }
	if errors.As(err, &se) {
		return se.GRPCStatus()
	}
	return nil
}

func statusText(err error) string {
	st := grpcStatus(err)
	if st == nil {
		return escape(err.Error())
	}
	return fmt.Sprintf("code = %s desc = %s", st.Code(), escape(st.Message()))
}

func sardErrorInfo(err error) *errdetails.ErrorInfo {
	st := grpcStatus(err)
	if st == nil {
		return nil
	}
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == sardErrorDomain {
			return info
		}
	}
	return nil
}

func metadata(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = escape(k) + "=" + escape(m[k])
	}
	return pairs
}

// escape writes control characters as Go does ("\n" is two characters).
func escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsControl(r) {
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
