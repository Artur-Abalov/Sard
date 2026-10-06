// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package transport

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/status"
)

// sardErrorDomain is the google.rpc.ErrorInfo domain of the server's
// reasons (docs/adr/0025-grpc-error-model.md).
const sardErrorDomain = "sard.dev"

// RefusalLine explains a permanent refusal (see IsPermanent) to the
// operator: the status code and text, the server's reason and metadata
// when it gave one, and that the agent will not retry. Control characters
// are escaped, so the result is one line.
func RefusalLine(err error) string {
	parts := []string{statusText(err)}
	if info := sardErrorInfo(err); info != nil {
		parts = append(parts, "reason "+escape(info.GetReason()))
		parts = append(parts, metadata(info.GetMetadata())...)
	}
	return strings.Join(parts, ", ") + "; not reconnecting: fix the cause, then restart the agent"
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
