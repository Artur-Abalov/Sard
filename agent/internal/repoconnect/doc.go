// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package repoconnect connects a repository to the host for "sard-agent repo
// add" (A8a, A8b; Р26 of docs/specs/agent/host-setup.feature): it finds out
// whether the repository exists, tries the password files it may be opened
// with, and either attaches the repository or creates it. What touches the
// host (the file system, restic, the source of a password, randomness)
// comes in through the Connector, so the logic is tested with fakes. The
// command line, the summary and the writing of the fragment stay with
// agent/cmd/sard-agent; hostsetup is a leaf and does not import this
// package.
package repoconnect
