-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- S6a: sources, runs of a source and their steps (ADR 0013 "Целевая схема" with the D6
-- amendment, ADR 0022; dispatch rules in the S6a ADR draft). History is never deleted:
-- runs and run_steps have no DELETE path, sources are deleted softly (ADR 0013, rule 3).

-- Created for runs.workflow_id (D6: NULL only for a manual run); no service uses it yet.
CREATE TABLE workflows (
    id         UUID        NOT NULL PRIMARY KEY,
    tenant_id  UUID        NOT NULL REFERENCES tenants (id),
    name       TEXT        NOT NULL,
    definition JSONB       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ,
    CONSTRAINT workflows_tenant_id_id_key UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX workflows_tenant_id_name_key ON workflows (tenant_id, name) WHERE deleted_at IS NULL;

-- config holds secret names only, never values (ADR 0008); repository_name is a name from the
-- agent's last Register, checked by the service (ADR 0022).
CREATE TABLE sources (
    id              UUID        NOT NULL PRIMARY KEY,
    tenant_id       UUID        NOT NULL REFERENCES tenants (id),
    agent_id        UUID        NOT NULL,
    name            TEXT        NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    plugin          TEXT        NOT NULL CHECK (plugin ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
    config          JSONB       NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    repository_name TEXT        NOT NULL CHECK (repository_name ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'),
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    deleted_at      TIMESTAMPTZ,
    CONSTRAINT sources_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT sources_agent_fkey FOREIGN KEY (tenant_id, agent_id) REFERENCES agents (tenant_id, id)
);
CREATE UNIQUE INDEX sources_tenant_id_name_key ON sources (tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX sources_tenant_id_agent_id_idx ON sources (tenant_id, agent_id);

-- Stage 1 runs a source (ADR 0022); runs.schedule_id arrives with the schedules table.
CREATE TABLE runs (
    id          UUID        NOT NULL PRIMARY KEY,
    tenant_id   UUID        NOT NULL REFERENCES tenants (id),
    source_id   UUID        NOT NULL,
    workflow_id UUID,
    trigger     TEXT        NOT NULL CHECK (trigger IN ('schedule', 'manual', 'verification')),
    status      TEXT        NOT NULL
        CHECK (status IN ('queued', 'dispatched', 'running', 'succeeded', 'failed', 'cancelled')),
    definition  JSONB,
    message     TEXT,
    queued_at   TIMESTAMPTZ NOT NULL,
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    CONSTRAINT runs_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT runs_source_fkey FOREIGN KEY (tenant_id, source_id) REFERENCES sources (tenant_id, id),
    CONSTRAINT runs_workflow_fkey FOREIGN KEY (tenant_id, workflow_id) REFERENCES workflows (tenant_id, id),
    CONSTRAINT runs_workflow_check CHECK (workflow_id IS NOT NULL OR trigger = 'manual'),
    CONSTRAINT runs_definition_check CHECK ((workflow_id IS NULL) = (definition IS NULL)),
    CONSTRAINT runs_finished_check CHECK ((status IN ('queued', 'dispatched', 'running')) = (finished_at IS NULL))
);
-- D6: at most one active run per source. The database holds it, not the code (ADR 0022).
CREATE UNIQUE INDEX runs_active_source_key ON runs (tenant_id, source_id)
    WHERE status IN ('queued', 'dispatched', 'running');
CREATE INDEX runs_tenant_id_source_id_queued_at_idx ON runs (tenant_id, source_id, queued_at DESC);
CREATE INDEX runs_tenant_id_workflow_id_queued_at_idx ON runs (tenant_id, workflow_id, queued_at DESC);

-- One command to an agent; id is RunStep.command_id. config is RunStep.config_json as sent:
-- sources.config may change after the run started.
CREATE TABLE run_steps (
    id              UUID        NOT NULL PRIMARY KEY,
    tenant_id       UUID        NOT NULL REFERENCES tenants (id),
    run_id          UUID        NOT NULL,
    ordinal         INTEGER     NOT NULL CHECK (ordinal >= 0),
    agent_id        UUID        NOT NULL,
    source_id       UUID,
    plugin          TEXT        NOT NULL,
    action          TEXT        NOT NULL CHECK (action IN ('backup', 'restore', 'verify', 'run')),
    repository_name TEXT,
    snapshot_id     TEXT,
    config          JSONB       NOT NULL,
    status          TEXT        NOT NULL CHECK (status IN ('queued', 'dispatched', 'running',
                                                           'succeeded', 'failed', 'cancelled', 'timed_out',
                                                           'rejected', 'lost')),
    phase           TEXT CHECK (phase IN ('accepted', 'preparing', 'dumping', 'uploading', 'restoring',
                                          'verifying')),
    bytes_processed BIGINT CHECK (bytes_processed >= 0),
    bytes_total     BIGINT CHECK (bytes_total >= 0),
    message         TEXT,
    output          JSONB,
    queued_at       TIMESTAMPTZ NOT NULL,
    dispatched_at   TIMESTAMPTZ,
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    CONSTRAINT run_steps_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT run_steps_tenant_id_run_id_ordinal_key UNIQUE (tenant_id, run_id, ordinal),
    CONSTRAINT run_steps_run_fkey FOREIGN KEY (tenant_id, run_id) REFERENCES runs (tenant_id, id),
    CONSTRAINT run_steps_agent_fkey FOREIGN KEY (tenant_id, agent_id) REFERENCES agents (tenant_id, id),
    CONSTRAINT run_steps_source_fkey FOREIGN KEY (tenant_id, source_id) REFERENCES sources (tenant_id, id),
    CONSTRAINT run_steps_source_check CHECK ((action = 'run') = (source_id IS NULL)),
    CONSTRAINT run_steps_repository_check CHECK ((action = 'run') = (repository_name IS NULL)),
    CONSTRAINT run_steps_dispatched_check CHECK ((status = 'queued') = (dispatched_at IS NULL)),
    CONSTRAINT run_steps_finished_check CHECK ((status IN ('queued', 'dispatched', 'running')) = (finished_at IS NULL))
);
-- Delivery on connect and reconciliation on Hello: an agent's active steps in creation order.
CREATE INDEX run_steps_active_agent_idx ON run_steps (tenant_id, agent_id, queued_at, id)
    WHERE status IN ('queued', 'dispatched', 'running');
