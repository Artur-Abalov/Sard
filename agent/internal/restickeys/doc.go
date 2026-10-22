// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package restickeys holds the decisions about the keys of a restic repository
// (V3, docs/specs/agent/restic-keys.feature): what the user field of a key
// says about its purpose and label (K1), which flags are valid, whether a key
// exists already (K10), which warnings a list of keys earns (K13) and which
// key an id prefix names (K14). It runs nothing and reads no file.
package restickeys
