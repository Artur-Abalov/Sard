// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package certrenew renews the agent's certificate before it expires
// (docs/specs/agent/agent-certificate-renewal.feature): a new key, a
// certificate for it from the server, the pair written next to the current
// one, the server's confirmation, and only then the pair becomes the current
// one. The renewal runs on connections of its own and never touches the
// stream or the steps.
package certrenew
