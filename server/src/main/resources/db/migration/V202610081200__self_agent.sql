-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- F5, phase 3 (docs/specs/server/self-agent.feature): the agent next to the server enrolls itself
-- with a built-in token the server writes into a channel shared with it. A built-in token is an
-- ordinary one-time token that REST never lists or touches; the agent it enrolls is built in, and a
-- tenant has at most one such agent that is not revoked.
ALTER TABLE enrollment_tokens ADD COLUMN builtin BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE agents ADD COLUMN builtin BOOLEAN NOT NULL DEFAULT false;
CREATE UNIQUE INDEX agents_one_live_builtin ON agents (tenant_id) WHERE builtin AND revoked_at IS NULL;
