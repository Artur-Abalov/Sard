// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/enroll"
)

// enrollClassCode is one row of enrollClassCodes: an enroll.Class, the
// exit code A2b maps it to, and the word and sentence --help prints for
// that code (F8, F12) — one table both enrollExitCode and printEnrollHelp
// read, so the two can never say something different about the same code.
type enrollClassCode struct {
	class enroll.Class
	code  int
	word  string
	help  string
}

// enrollClassCodes has exactly one row per enroll.Class (В3, В12 of the
// task; checked by TestEveryEnrollClassHasAnExitCodeAndTheHelpPrintsIt).
// Codes 0 (success) and 4 (identity exists) have no enroll.Class — an
// identity-exists refusal is a local check A2b makes itself, before ever
// classifying a server or network outcome — and are added separately in
// enrollHelpCodes.
var enrollClassCodes = []enrollClassCode{
	{enroll.ClassAgentError, exitAgentError, "agent error", "an unforeseen server response, or a local problem only an updated agent fixes"},
	{enroll.ClassUsage, exitUsage, "usage", "bad flags, token source, config, token format, a --server/config address mismatch, or a user who is neither root nor the service user (PRIVILEGES_REQUIRED, SERVICE_USER_UNKNOWN)"},
	{enroll.ClassTokenRefused, exitTokenRefused, "token refused", "TOKEN_UNKNOWN, TOKEN_USED, TOKEN_EXPIRED, or TOKEN_REVOKED"},
	{enroll.ClassTrust, exitTrust, "trust", "the server's CA fingerprint or hostname could not be verified"},
	{enroll.ClassTemporary, exitTemporary, "temporary", "the server was unreachable, timed out, or another enrollment is already running"},
	{enroll.ClassWrite, exitWrite, "write", "a target directory or file could not be written"},
}

// enrollExitCode is the single place A2b maps an enroll.Class to an exit
// code (В3, В12 of the task).
func enrollExitCode(class enroll.Class) int {
	for _, c := range enrollClassCodes {
		if c.class == class {
			return c.code
		}
	}
	return exitAgentError
}

// enrollHelpCodes is every exit code 0-7, in order, for --help: the six
// enrollClassCodes rows plus the two that are not an enroll.Class.
func enrollHelpCodes() []enrollClassCode {
	all := make([]enrollClassCode, 0, len(enrollClassCodes)+2)
	all = append(all, enrollClassCode{code: exitOK, word: "success", help: "identity written, or --help"})
	all = append(all, enrollClassCodes...)
	all = append(all, enrollClassCode{code: exitIdentityExists, word: "identity exists", help: "this host already has a key or certificate; use --force"})
	sort.Slice(all, func(i, j int) bool { return all[i].code < all[j].code })
	return all
}

func reportEnrollError(stderr io.Writer, err error) int {
	var eerr *enroll.Error
	if !errors.As(err, &eerr) {
		_, _ = fmt.Fprintf(stderr, "sard-agent enroll: %v\n", err)
		return exitAgentError
	}
	_, _ = fmt.Fprintf(stderr, "sard-agent enroll: %s\n", enrollMessage(eerr))
	return enrollExitCode(eerr.Class)
}

// enrollMessage adds what *enroll.Error's own Error() text leaves out — the
// address, server certificate names, gRPC code and "token maybe spent"
// warning are structured fields, not part of the string A2a builds — plus,
// for reasons A2a does not itself explain, what each one means to the
// operator and whether retrying with the same token can help.
func enrollMessage(e *enroll.Error) string {
	return e.Error() + reasonMeaningSuffix(e) + addressSuffix(e) + namesSuffix(e) + codeSuffix(e) + spentSuffix(e)
}

// reasonMeaningSuffix skips ClassAgentError: its own msg (В4) already says
// what reasonMeaning would, in the agent-fault wording, not the "get a new
// token" wording reasonMeaning uses for the rest.
func reasonMeaningSuffix(e *enroll.Error) string {
	if e.Class == enroll.ClassAgentError {
		return ""
	}
	if meaning := reasonMeaning(e.Reason); meaning != "" {
		return ": " + meaning
	}
	return ""
}

func addressSuffix(e *enroll.Error) string {
	if e.Address == "" {
		return ""
	}
	return fmt.Sprintf(" (server address %s)", e.Address)
}

func namesSuffix(e *enroll.Error) string {
	if len(e.Names) == 0 {
		return ""
	}
	return "; server certificate names: " + strings.Join(e.Names, ", ")
}

func codeSuffix(e *enroll.Error) string {
	if e.Code == "" {
		return ""
	}
	return fmt.Sprintf("; gRPC code %s", e.Code)
}

func spentSuffix(e *enroll.Error) string {
	if !e.TokenMaybeSpent {
		return ""
	}
	return "; the token may have been spent by this attempt — if a retry is refused with TOKEN_USED, get a new token"
}

// reasonMeanings explains a server refusal reason and whether retrying with
// the same token can help (rule "Отказы сервера объясняются по причине"). A
// reason absent from the table (default "") means reasonMeaning has nothing
// to add.
var reasonMeanings = map[string]string{
	"TOKEN_UNKNOWN":   "this token was not issued by this server; retrying with it will not help",
	"TOKEN_USED":      "this token has already been used; retrying with it will not help, get a new token",
	"TOKEN_EXPIRED":   "this token has expired; retrying with it will not help, get a new token",
	"TOKEN_REVOKED":   "this token was revoked in the console; retrying with it will not help",
	"TOKEN_MALFORMED": "the token string looks corrupted from copying; retrying with it will not help",
}

func reasonMeaning(reason string) string {
	return reasonMeanings[reason]
}
