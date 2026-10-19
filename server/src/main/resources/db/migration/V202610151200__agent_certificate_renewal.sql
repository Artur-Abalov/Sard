-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- C1 (docs/specs/server/agent-certificate-renewal.feature): a renewed certificate remembers the one it
-- was issued against, and a confirmed renewal supersedes the old certificate. Existing rows get NULL in
-- both columns: no predecessor, not superseded, so they keep working until their not_after.
ALTER TABLE agent_certificates ADD COLUMN predecessor_serial TEXT;
ALTER TABLE agent_certificates ADD COLUMN superseded_at TIMESTAMPTZ;

-- The predecessor is a certificate of the same tenant and agent: the foreign key spans all three columns.
ALTER TABLE agent_certificates
    ADD CONSTRAINT agent_certificates_serial_agent_key UNIQUE (serial, tenant_id, agent_id);
ALTER TABLE agent_certificates
    ADD CONSTRAINT agent_certificates_predecessor_fkey FOREIGN KEY (predecessor_serial, tenant_id, agent_id)
    REFERENCES agent_certificates (serial, tenant_id, agent_id);
