-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- S4a: the snapshot an agent announces in Register (ADR 0013, "Целевая схема"; ADR 0008:
-- names and metadata only). Each Register replaces the agent's columns and both sets whole.

-- NULL until the first Register; then all of them at once.
ALTER TABLE agents
    ADD COLUMN os               TEXT,
    ADD COLUMN arch             TEXT,
    ADD COLUMN protocol_version INTEGER CHECK (protocol_version > 0),
    ADD COLUMN secret_names     TEXT[]  NOT NULL DEFAULT '{}',
    ADD COLUMN script_names     TEXT[]  NOT NULL DEFAULT '{}',
    ADD COLUMN last_register_at TIMESTAMPTZ,
    ADD CONSTRAINT agents_register_check CHECK (
        last_register_at IS NULL
        OR (os IS NOT NULL AND arch IS NOT NULL AND protocol_version IS NOT NULL AND agent_version IS NOT NULL)
    );

-- The schema lives with the plugin row of its own tenant: no table shared across tenants,
-- so no agent can shape what another tenant's console renders (S4a decision 1).
CREATE TABLE agent_plugins (
    tenant_id     UUID   NOT NULL REFERENCES tenants (id),
    agent_id      UUID   NOT NULL,
    name          TEXT   NOT NULL CHECK (name ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
    version       TEXT   NOT NULL,
    config_schema JSONB  NOT NULL,
    actions       TEXT[] NOT NULL CHECK (actions <@ ARRAY['backup', 'restore', 'verify', 'run']),
    CONSTRAINT agent_plugins_pkey PRIMARY KEY (agent_id, name),
    CONSTRAINT agent_plugins_agent_fkey FOREIGN KEY (tenant_id, agent_id) REFERENCES agents (tenant_id, id)
);

-- repository_id is restic's own id (not a secret); NULL when the agent could not read it.
-- crypto_provider NULL means restic's built-in AES (ADR 0008).
CREATE TABLE agent_repositories (
    tenant_id       UUID NOT NULL REFERENCES tenants (id),
    agent_id        UUID NOT NULL,
    name            TEXT NOT NULL CHECK (name ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
    backend         TEXT NOT NULL CHECK (backend ~ '^[a-z][a-z0-9]{0,15}$'),
    repository_id   TEXT CHECK (repository_id ~ '^[0-9a-f]{64}$'),
    crypto_provider TEXT CHECK (crypto_provider ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
    CONSTRAINT agent_repositories_pkey PRIMARY KEY (agent_id, name),
    CONSTRAINT agent_repositories_agent_fkey FOREIGN KEY (tenant_id, agent_id) REFERENCES agents (tenant_id, id)
);

-- Key holders of a repository within the tenant (ADR 0008: a key on a single host is flagged).
CREATE INDEX agent_repositories_tenant_id_repository_id_idx ON agent_repositories (tenant_id, repository_id);
