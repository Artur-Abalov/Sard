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

-- Monthly partitions (DB2, docs/adr/0069-database-roles.md). The server is sard_app and may not create or drop a
-- table, so two functions owned by sard_owner (SECURITY DEFINER) do it for it, and nothing else: they take a month or
-- the name of a partition of step_logs, and they wait for a lock at most two seconds, so a reader of the table does
-- not make the writing of log lines of every tenant wait for long. The drop does not use DETACH ... CONCURRENTLY:
-- PostgreSQL refuses it inside a function or a procedure. A lock that is not got in time is an error, and the caller
-- tries again at its next pass. Both take the same advisory lock, so servers that start together make a partition
-- once and drop it once. There is no DEFAULT partition: a line of a month without a partition fails its chunk, which
-- is how a server that was down too long shows it.
CREATE FUNCTION create_step_log_partition(month DATE) RETURNS TEXT
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = pg_catalog, sard
    SET lock_timeout = '2s'
AS $$
DECLARE
    first_day DATE := date_trunc('month', month)::date;
    partition_name TEXT := 'step_logs_' || to_char(first_day, 'YYYY_MM');
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('sard.step_logs.partitions', 0));
    IF to_regclass(format('sard.%I', partition_name)) IS NOT NULL THEN
        RETURN NULL;
    END IF;
    EXECUTE format(
        'CREATE TABLE sard.%I PARTITION OF sard.step_logs FOR VALUES FROM (%L) TO (%L)',
        partition_name,
        first_day::timestamp AT TIME ZONE 'UTC',
        (first_day + interval '1 month')::timestamp AT TIME ZONE 'UTC');
    RETURN partition_name;
END
$$;

CREATE FUNCTION drop_step_log_partition(partition_name TEXT) RETURNS BOOLEAN
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = pg_catalog, sard
    SET lock_timeout = '2s'
AS $$
BEGIN
    IF partition_name !~ '^step_logs_[0-9]{4}_[0-9]{2}$' THEN
        RAISE EXCEPTION 'not a log partition: %', partition_name USING ERRCODE = 'invalid_parameter_value';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('sard.step_logs.partitions', 0));
    IF NOT EXISTS (SELECT FROM pg_inherits
                   WHERE inhparent = 'sard.step_logs'::regclass
                     AND inhrelid = to_regclass(format('sard.%I', partition_name))) THEN
        RETURN FALSE;
    END IF;
    EXECUTE format('ALTER TABLE sard.step_logs DETACH PARTITION sard.%I', partition_name);
    EXECUTE format('DROP TABLE sard.%I', partition_name);
    RETURN TRUE;
END
$$;

-- The month the migration runs in and the one after it; the server makes the next ones ahead (sard.logs).
SELECT create_step_log_partition(date_trunc('month', now() AT TIME ZONE 'UTC')::date);
SELECT create_step_log_partition((date_trunc('month', now() AT TIME ZONE 'UTC') + interval '1 month')::date);

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
