// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// What the server expects as `confirm` to revoke the built-in agent (docs/specs/server/self-agent.feature).
// The server compares it and refuses without it; the console only makes the administrator type it.
export const SELF_AGENT_CONFIRMATION = 'sard-self'

// Whether the revoke button of the dialog may be pressed: any agent but a built-in one at once, a
// built-in one after the exact text is typed.
export function canConfirmRevoke(builtin: boolean, typed: string): boolean {
  return !builtin || typed === SELF_AGENT_CONFIRMATION
}

// The query of the revoke request: `confirm` for a built-in agent, nothing for any other.
export function revokeQuery(builtin: boolean, typed: string): { confirm?: string } {
  return builtin ? { confirm: typed } : {}
}
