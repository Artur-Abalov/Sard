// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"fmt"
	"strings"

	"github.com/Artur-Abalov/sard/agent/internal/certpair"
	"github.com/Artur-Abalov/sard/agent/internal/config"
	"github.com/Artur-Abalov/sard/agent/internal/transport"
)

// builtInHint replaces the re-enrollment command for the built-in agent,
// which enrolls itself again once the console has revoked it (П24).
const builtInHint = "revoke the built-in agent in the console; it enrolls again by itself"

// refusedError is an end of the agent that no restart cures and that exit
// code 78 reports: the server refused the agent for good, or its certificate
// has expired (docs/specs/agent/agent-certificate-renewal.feature, П22).
type refusedError struct {
	// detail says what is wrong, on one line.
	detail string
	// reenroll is the command that cures it; empty when none does.
	reenroll string
	cause    error
}

func (e *refusedError) Error() string { return e.line(false) }

func (e *refusedError) Unwrap() error { return e.cause }

// line is the text after "sard-agent: ". A daemon is told the command and
// that it will not reconnect; the built-in agent only what to do in the
// console, for it does not exit.
func (e *refusedError) line(builtIn bool) string {
	parts := []string{e.detail}
	switch {
	case e.reenroll == "":
	case builtIn:
		parts = append(parts, builtInHint)
	default:
		parts = append(parts, e.reenroll)
	}
	text := strings.Join(parts, ", ")
	if builtIn {
		return text
	}
	return text + "; " + transport.RefusalSuffix
}

// reenrollCommand is what the operator runs to get a new certificate (П22).
func reenrollCommand(configPath string, cfg config.Config) string {
	return fmt.Sprintf("sudo sard-agent enroll --force --config %s --server %s --token <TOKEN>", configPath, cfg.Server.Address)
}

// expiredRefusal is the refusal of a certificate that has expired.
func expiredRefusal(e *certpair.ExpiredError, configPath string, cfg config.Config) *refusedError {
	return &refusedError{detail: e.Error(), reenroll: reenrollCommand(configPath, cfg), cause: e}
}

// refuse turns a permanent refusal of the server into a refusedError; the
// command is added for the reasons a new certificate cures. Other errors pass.
func refuse(err error, configPath string, cfg config.Config) error {
	if !transport.IsPermanent(err) {
		return err
	}
	r := &refusedError{detail: transport.RefusalDetail(err), cause: err}
	switch transport.RefusalReason(err) {
	case "CERT_EXPIRED", "CERT_SUPERSEDED":
		r.reenroll = reenrollCommand(configPath, cfg)
	}
	return r
}
