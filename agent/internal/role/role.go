// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Package role is what the server allows an agent to do
// (docs/specs/agent/agent-role.feature). It is a leaf: the certificate that
// carries the role is read elsewhere.
package role

import agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"

// Role is what the server allows the agent to do.
type Role string

const (
	// Source runs BACKUP, RESTORE, RUN, FORGET and PRUNE.
	Source Role = "source"
	// Verifier runs VERIFY only.
	Verifier Role = "verifier"
)

// Valid reports whether r is one of the two roles.
func (r Role) Valid() bool { return r == Source || r == Verifier }

// Allows reports whether the role may run the action. ACTION_UNSPECIFIED and
// unknown actions pass: the executor's plugin check rejects them as before.
func (r Role) Allows(a agentv1.Action) bool {
	switch a {
	case agentv1.Action_ACTION_BACKUP, agentv1.Action_ACTION_RESTORE, agentv1.Action_ACTION_RUN, agentv1.Action_ACTION_FORGET, agentv1.Action_ACTION_PRUNE:
		return r == Source
	case agentv1.Action_ACTION_VERIFY:
		return r == Verifier
	}
	return true
}
