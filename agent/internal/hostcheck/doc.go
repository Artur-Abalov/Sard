// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package hostcheck is what the host checks of the agent (sard-agent repo
// check and sard-agent check; docs/specs/agent/host-check.feature) share: the
// line of a check with its status, the text and the JSON Lines output, the
// tally that gives the exit code, the reading of restic locks, the record of
// the repositories the service announced to the server and the judgement of
// the facts of the host (permissions, certificate, config, secrets, the
// programs of PostgreSQL, SELinux, the service).
// It decides nothing about the host: the commands collect the facts and
// pass them in.
package hostcheck
