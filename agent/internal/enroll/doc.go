// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package enroll is the mechanism behind "sard-agent enroll" (A2a): parsing
// the enrollment token, generating a key and CSR, verifying the server by
// the CA fingerprint pinned in the token (trust on first use, ADR 0014),
// calling EnrollmentService.Enroll, classifying the outcome and writing the
// resulting identity atomically to disk.
//
// The CLI surface (flags, messages, exit codes) is A2b, layered on top of
// this package; see docs/specs/agent/agent-enroll.feature.
package enroll
