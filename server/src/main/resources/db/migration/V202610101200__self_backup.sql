-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- F6: the self-backup (ADR 00XX-draft-self-backup). The server keeps two system sources of its own
-- built-in agent: the database and the keys with the configuration. They go to the repository the
-- administrator bound; storage credentials and the repository password stay on the agent (ADR 0008).

-- system_role marks a source the server creates and keeps; the API refuses to replace or delete it.
-- At most one live source per role and tenant.
ALTER TABLE sources ADD COLUMN system_role TEXT
    CONSTRAINT sources_system_role_check CHECK (system_role IN ('self_database', 'self_keys'));
CREATE UNIQUE INDEX sources_tenant_id_system_role_key ON sources (tenant_id, system_role)
    WHERE deleted_at IS NULL AND system_role IS NOT NULL;

-- A system source does not take a name from the administrator's sources: names stay unique among
-- the administrator's own.
DROP INDEX sources_tenant_id_name_key;
CREATE UNIQUE INDEX sources_tenant_id_name_key ON sources (tenant_id, name)
    WHERE deleted_at IS NULL AND system_role IS NULL;

-- The binding itself: when the sources last moved to a repository (or agent) and whether a local
-- repository was confirmed (D10). The agent and repository are those of the system sources.
CREATE TABLE self_backups (
    id                      UUID        NOT NULL PRIMARY KEY,
    tenant_id               UUID        NOT NULL REFERENCES tenants (id),
    local_storage_confirmed BOOLEAN     NOT NULL,
    bound_at                TIMESTAMPTZ NOT NULL,
    updated_at              TIMESTAMPTZ NOT NULL,
    CONSTRAINT self_backups_tenant_id_key UNIQUE (tenant_id)
);
