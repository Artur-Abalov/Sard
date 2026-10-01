// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package repoinit decides what "sard-agent repo init" and "sard-agent repo
// list" (A5b, docs/specs/agent/repo-init.feature) do with a repository of
// the agent config: which local checks come before the backend, what an
// answer of restic means, and which failure the operator is told about.
// The command line, the messages' framing and the exit codes are
// agent/cmd/sard-agent; restic itself is agent/internal/restic.
package repoinit
