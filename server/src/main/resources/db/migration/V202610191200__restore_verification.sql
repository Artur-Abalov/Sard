-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- V4: restore verification on the server (ADR 00XX-draft-verification-server, design
-- docs/design/sard-restore-verification.md). A verification is a run of its own (trigger
-- 'verification') with one VERIFY step on a verifier agent: delivery, progress, results and step loss
-- are those of every run. Two tables of its own: the queue of jobs and the history of verifications.

-- A verification run is not a backup: D6 (one active run per source, ADR 0022) holds for backups only,
-- so a backup and a verification of the same source run side by side.
DROP INDEX runs_active_source_key;
CREATE UNIQUE INDEX runs_active_source_key ON runs (tenant_id, source_id)
    WHERE status IN ('queued', 'dispatched', 'running') AND trigger <> 'verification';
ALTER TABLE runs DROP CONSTRAINT runs_workflow_check;
ALTER TABLE runs ADD CONSTRAINT runs_workflow_check
    CHECK (workflow_id IS NOT NULL OR trigger IN ('manual', 'schedule', 'catch_up', 'verification'));

-- What a VERIFY step carries besides its snapshot (RunStep.verify): the job, the repository's restic id,
-- the fingerprint and its hash, the deadline. Copied as sent, like config.
ALTER TABLE run_steps ADD COLUMN verify_task JSONB;

-- Whether the source's latest snapshot is verified every cycle. On by default (owner decision, V4).
ALTER TABLE sources ADD COLUMN verification_enabled BOOLEAN NOT NULL DEFAULT TRUE;

-- What the agent's last Register said about verification (RegisterRequest.verifier). verifier_reported is
-- false for an agent older than V4, which is never handed a verification; free bytes and sandbox are NULL
-- while the agent does not know them.
ALTER TABLE agents ADD COLUMN verifier_reported BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE agents ADD COLUMN verify_free_bytes BIGINT CHECK (verify_free_bytes >= 0);
ALTER TABLE agents ADD COLUMN verify_sandbox TEXT CHECK (verify_sandbox IN ('none', 'docker', 'podman'));

-- The daily cycle of the installation (V-D9): one schedule for all tenants, set through REST; without a row the
-- defaults apply (sard.verification.*). Installation-wide like onboarding_steps, so it has no tenant_id. A cycle
-- fires at each cron moment: every source whose verification is on and that has no cycle job since that moment
-- gets one, so a cycle is never fired twice and never needs a lock of its own.
CREATE TABLE verification_schedule (
    singleton  BOOLEAN     NOT NULL PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    cron       TEXT        NOT NULL CHECK (length(cron) BETWEEN 1 AND 200),
    timezone   TEXT        NOT NULL CHECK (length(timezone) BETWEEN 1 AND 64),
    updated_at TIMESTAMPTZ NOT NULL
);

-- The queue (V-D8). A job verifies one snapshot of one source; at most one is open per source (V-D9).
-- queued: waiting for a suitable verifier until deadline; running: run_id is its current attempt on
-- verifier_agent_id; closed: its verification is recorded. failed_attempts counts attempts in a row that
-- ended without a verdict (lost, rejected, timed out); the third closes the job as an infrastructure
-- failure. unsuitable is why each verifier did not fit at the last try, for REST and V6.
CREATE TABLE verification_jobs (
    id                 UUID        NOT NULL PRIMARY KEY,
    tenant_id          UUID        NOT NULL REFERENCES tenants (id),
    source_id          UUID        NOT NULL,
    snapshot_ref       UUID        NOT NULL,
    repository_id      TEXT        NOT NULL CHECK (repository_id ~ '^[0-9a-f]{64}$'),
    plugin             TEXT        NOT NULL,
    fingerprint        BYTEA,
    fingerprint_sha256 TEXT CHECK (fingerprint_sha256 ~ '^[0-9a-f]{64}$'),
    reason             TEXT        NOT NULL CHECK (reason IN ('cycle', 'manual')),
    status             TEXT        NOT NULL CHECK (status IN ('queued', 'running', 'closed')),
    failed_attempts    SMALLINT    NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    run_id             UUID,
    verifier_agent_id  UUID,
    unsuitable         JSONB,
    created_at         TIMESTAMPTZ NOT NULL,
    deadline           TIMESTAMPTZ NOT NULL,
    closed_at          TIMESTAMPTZ,
    CONSTRAINT verification_jobs_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT verification_jobs_source_fkey FOREIGN KEY (tenant_id, source_id) REFERENCES sources (tenant_id, id),
    CONSTRAINT verification_jobs_snapshot_fkey FOREIGN KEY (tenant_id, snapshot_ref, source_id)
        REFERENCES snapshots (tenant_id, id, source_id),
    CONSTRAINT verification_jobs_run_fkey FOREIGN KEY (tenant_id, run_id) REFERENCES runs (tenant_id, id),
    CONSTRAINT verification_jobs_verifier_fkey FOREIGN KEY (tenant_id, verifier_agent_id)
        REFERENCES agents (tenant_id, id),
    CONSTRAINT verification_jobs_fingerprint_check CHECK ((fingerprint IS NULL) = (fingerprint_sha256 IS NULL)),
    CONSTRAINT verification_jobs_running_check CHECK (status <> 'running' OR run_id IS NOT NULL),
    CONSTRAINT verification_jobs_closed_check CHECK ((status = 'closed') = (closed_at IS NOT NULL))
);
CREATE UNIQUE INDEX verification_jobs_open_key ON verification_jobs (tenant_id, source_id) WHERE status <> 'closed';
-- The cycle's check «a cycle job since this moment» per source.
CREATE INDEX verification_jobs_tenant_id_source_id_created_at_idx ON verification_jobs (tenant_id, source_id, created_at DESC);

-- The history (ADR 0013): one row per finished job. Immutable. level is the highest level
-- reached (0-5); stages and mismatches are the verifier's evidence; unsuitable is why no verifier fit.
-- key_holders is how many live agents of the tenant report the repository's id (ADR 0008: a key on one
-- host only is flagged). on_origin_host is not stored: it is verifier_agent_id = snapshots.agent_id.
CREATE TABLE restore_verifications (
    id                  UUID        NOT NULL PRIMARY KEY,
    tenant_id           UUID        NOT NULL REFERENCES tenants (id),
    job_id              UUID        NOT NULL,
    source_id           UUID        NOT NULL,
    snapshot_id         UUID        NOT NULL,
    step_id             UUID,
    verifier_agent_id   UUID,
    key_holders         SMALLINT    NOT NULL CHECK (key_holders >= 0),
    status              TEXT        NOT NULL CHECK (status IN ('passed', 'failed', 'cannot_verify',
                                                               'infrastructure_failure', 'no_verifier')),
    level               SMALLINT    NOT NULL CHECK (level BETWEEN 0 AND 5),
    reason              TEXT,
    stages              JSONB       NOT NULL DEFAULT '[]',
    mismatches          JSONB       NOT NULL DEFAULT '[]',
    unsuitable          JSONB,
    restore_duration_ms BIGINT CHECK (restore_duration_ms >= 0),
    verified_at         TIMESTAMPTZ NOT NULL,
    CONSTRAINT restore_verifications_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT restore_verifications_tenant_id_job_id_key UNIQUE (tenant_id, job_id),
    CONSTRAINT restore_verifications_job_fkey FOREIGN KEY (tenant_id, job_id) REFERENCES verification_jobs (tenant_id, id),
    CONSTRAINT restore_verifications_snapshot_fkey FOREIGN KEY (tenant_id, snapshot_id, source_id)
        REFERENCES snapshots (tenant_id, id, source_id),
    CONSTRAINT restore_verifications_step_fkey FOREIGN KEY (tenant_id, step_id) REFERENCES run_steps (tenant_id, id),
    CONSTRAINT restore_verifications_verifier_fkey FOREIGN KEY (tenant_id, verifier_agent_id)
        REFERENCES agents (tenant_id, id),
    CONSTRAINT restore_verifications_verdict_check
        CHECK (status IN ('infrastructure_failure', 'no_verifier') OR (step_id IS NOT NULL AND verifier_agent_id IS NOT NULL)),
    CONSTRAINT restore_verifications_passed_check CHECK (status <> 'passed' OR level >= 4)
);
-- The main metric: the last passed verification of a source.
CREATE INDEX restore_verifications_passed_idx ON restore_verifications (tenant_id, source_id, verified_at DESC)
    WHERE status = 'passed';
CREATE INDEX restore_verifications_source_idx ON restore_verifications (tenant_id, source_id, verified_at DESC);
