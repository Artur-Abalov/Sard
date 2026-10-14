-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- V1 (docs/specs/server/agent-role.feature): an agent has an immutable role, chosen when its
-- enrollment token is issued (source or verifier), and a machine id its Register reports. Existing
-- rows get "source" only so that the schema applies; their certificates carry no role URI and
-- are refused (CERT_ROLE_MISSING), so those agents enroll again. No column keeps a default.
ALTER TABLE enrollment_tokens ADD COLUMN role TEXT NOT NULL DEFAULT 'source';
ALTER TABLE enrollment_tokens ALTER COLUMN role DROP DEFAULT;
ALTER TABLE enrollment_tokens ADD CONSTRAINT enrollment_tokens_role_check CHECK (role IN ('source', 'verifier'));
-- The agent next to the server is always a source.
ALTER TABLE enrollment_tokens ADD CONSTRAINT enrollment_tokens_builtin_source_check CHECK (NOT builtin OR role = 'source');

ALTER TABLE agents ADD COLUMN role TEXT NOT NULL DEFAULT 'source';
ALTER TABLE agents ALTER COLUMN role DROP DEFAULT;
ALTER TABLE agents ADD CONSTRAINT agents_role_check CHECK (role IN ('source', 'verifier'));
-- The agent next to the server is always a source.
ALTER TABLE agents ADD CONSTRAINT agents_builtin_source_check CHECK (NOT builtin OR role = 'source');
-- NULL until the agent's first Register after V1; never shown by REST. Otherwise the 64 hex digits of a SHA-256.
ALTER TABLE agents ADD COLUMN machine_id TEXT;
ALTER TABLE agents ADD CONSTRAINT agents_machine_id_check CHECK (machine_id IS NULL OR machine_id ~ '^[0-9a-f]{64}$');
CREATE INDEX agents_tenant_id_machine_id_idx ON agents (tenant_id, machine_id) WHERE machine_id IS NOT NULL;

-- A schedule that meets a source bound to a verifier is refused (Р8).
ALTER TABLE schedule_fires DROP CONSTRAINT schedule_fires_reason_check;
ALTER TABLE schedule_fires ADD CONSTRAINT schedule_fires_reason_check CHECK (
    reason IN ('source_deleted', 'agent_revoked', 'unknown_plugin', 'unknown_repository', 'agent_role_mismatch')
);
