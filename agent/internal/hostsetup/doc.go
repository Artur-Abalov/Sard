// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package hostsetup is what the commands that set up an agent host share
// (A8a, docs/specs/agent/host-setup.feature): who may run them, where a
// secret value comes from, atomic writes of files with an owner, the
// fragments of agent.d, the audit line, and applying a change by
// restarting the service only when no step is running.
//
// Everything that touches the host is behind an interface (FS, Systemd,
// Auditor, Terminal, LookupFunc), so a test drives it with fakes.
package hostsetup
