-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- S7a: what agents report about their steps (ADR 0013 "Целевая схема"; decisions in the S7a
-- session log and ADR draft). History is never deleted with the objects it describes (rule 3):
-- snapshots stay after their source is deleted, step_logs leave by whole partitions only.

-- Log lines of one step, numbered by the server (LogChunk carries no sequence). Partitioned by
-- the server's receive time, never the agent's clock: a wrong agent clock would find no
-- partition. The key includes the partition key, so partitions never change it.
CREATE TABLE step_logs (
    tenant_id   UUID        NOT NULL REFERENCES tenants (id),
    step_id     UUID        NOT NULL,
    seq         BIGINT      NOT NULL CHECK (seq >= 1),
    received_at TIMESTAMPTZ NOT NULL,
    time        TIMESTAMPTZ,
    level       TEXT        NOT NULL CHECK (level IN ('debug', 'info', 'warn', 'error')),
    text        TEXT        NOT NULL,
    CONSTRAINT step_logs_pkey PRIMARY KEY (tenant_id, step_id, seq, received_at),
    CONSTRAINT step_logs_step_fkey FOREIGN KEY (tenant_id, step_id) REFERENCES run_steps (tenant_id, id)
) PARTITION BY RANGE (received_at);

-- Monthly partitions; the server creates the next ones ahead (sard.logs), these cover the
-- month the migration runs in and the one after it.
DO $$
DECLARE
    month DATE;
BEGIN
    FOR i IN 0..1 LOOP
        month := (date_trunc('month', now() AT TIME ZONE 'UTC') + make_interval(months => i))::date;
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS %I PARTITION OF step_logs FOR VALUES FROM (%L) TO (%L)',
            'step_logs_' || to_char(month, 'YYYY_MM'),
            month::timestamp AT TIME ZONE 'UTC',
            (month + interval '1 month')::timestamp AT TIME ZONE 'UTC');
    END LOOP;
END
$$;

-- How much of a step's log the server kept: seq is taken from log_lines by a guarded update,
-- and the limits of sard.logs count against log_bytes. Written after the step is final too:
-- the agent sends a step's last lines after its result.
ALTER TABLE run_steps
    ADD COLUMN log_lines     BIGINT  NOT NULL DEFAULT 0 CHECK (log_lines >= 0),
    ADD COLUMN log_bytes     BIGINT  NOT NULL DEFAULT 0 CHECK (log_bytes >= 0),
    ADD COLUMN log_truncated BOOLEAN NOT NULL DEFAULT false;

-- restic snapshots created by Sard, one per successful backup step (also a late one after
-- the step was lost: the snapshot is real). repository_name is the step's: BackupOutput has
-- only restic's repository_id.
CREATE TABLE snapshots (
    id              UUID        NOT NULL PRIMARY KEY,
    tenant_id       UUID        NOT NULL REFERENCES tenants (id),
    source_id       UUID        NOT NULL,
    step_id         UUID        NOT NULL,
    agent_id        UUID        NOT NULL,
    repository_name TEXT        NOT NULL,
    repository_id   TEXT        NOT NULL CHECK (repository_id <> ''),
    snapshot_id     TEXT        NOT NULL CHECK (snapshot_id <> ''),
    total_bytes     BIGINT      NOT NULL CHECK (total_bytes >= 0),
    added_bytes     BIGINT      NOT NULL CHECK (added_bytes >= 0),
    created_at      TIMESTAMPTZ NOT NULL,
    forgotten_at    TIMESTAMPTZ,
    CONSTRAINT snapshots_tenant_id_id_key UNIQUE (tenant_id, id),
    -- The target of restore_verifications' FK (ADR 0013).
    CONSTRAINT snapshots_tenant_id_id_source_id_key UNIQUE (tenant_id, id, source_id),
    CONSTRAINT snapshots_tenant_id_repository_id_snapshot_id_key UNIQUE (tenant_id, repository_id, snapshot_id),
    CONSTRAINT snapshots_tenant_id_step_id_key UNIQUE (tenant_id, step_id),
    CONSTRAINT snapshots_source_fkey FOREIGN KEY (tenant_id, source_id) REFERENCES sources (tenant_id, id),
    CONSTRAINT snapshots_step_fkey FOREIGN KEY (tenant_id, step_id) REFERENCES run_steps (tenant_id, id),
    CONSTRAINT snapshots_agent_fkey FOREIGN KEY (tenant_id, agent_id) REFERENCES agents (tenant_id, id)
);
CREATE INDEX snapshots_tenant_id_source_id_created_at_idx ON snapshots (tenant_id, source_id, created_at DESC);
