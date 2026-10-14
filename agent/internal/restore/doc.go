// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package restore is what the agent knows about the snapshots of a
// repository: reading the list, the tags Sard writes on a snapshot
// (sard.source, sard.run, sard.step), the order and the time format of the
// list (docs/specs/agent/repo-restore.feature, Р54-Р57, Р68). The command
// line (package main) only reads flags and prints; the restore of A8c-2 and
// the RESTORE of plugins (R2) build on the same package. Everything that
// touches the host or restic goes through an interface.
package restore
