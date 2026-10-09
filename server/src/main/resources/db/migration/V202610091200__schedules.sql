-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- F3a: a schedule per source and the journal of its fires (D9, D16, ADR 00XX-draft-scheduler).
-- A schedule fires through the same run creation as a manual start; the fire, its run and the
-- schedule's move to the next fire commit together, so a fire is neither lost nor doubled.

-- A run started by a schedule has no workflow (ADR 0022 amended): 'schedule' for a fire on time,
-- 'catch_up' for the one run that stands for fires missed while the server was down (D16).
ALTER TABLE runs DROP CONSTRAINT runs_trigger_check;
ALTER TABLE runs ADD CONSTRAINT runs_trigger_check
    CHECK (trigger IN ('schedule', 'manual', 'verification', 'catch_up'));
ALTER TABLE runs DROP CONSTRAINT runs_workflow_check;
ALTER TABLE runs ADD CONSTRAINT runs_workflow_check
    CHECK (workflow_id IS NOT NULL OR trigger IN ('manual', 'schedule', 'catch_up'));

-- One schedule per source. next_run_at is the next fire by cron, NULL while disabled; catch_up_at
-- is when a catch-up owed after a downtime runs, spread so many sources do not start at once.
-- A deleted source's schedule stays and never fires: the scan joins live sources only.
CREATE TABLE schedules (
    id             UUID        NOT NULL PRIMARY KEY,
    tenant_id      UUID        NOT NULL REFERENCES tenants (id),
    source_id      UUID        NOT NULL,
    cron           TEXT        NOT NULL CHECK (length(cron) BETWEEN 9 AND 200),
    timezone       TEXT        NOT NULL CHECK (length(timezone) BETWEEN 1 AND 64),
    enabled        BOOLEAN     NOT NULL,
    next_run_at    TIMESTAMPTZ,
    catch_up_at    TIMESTAMPTZ,
    last_fired_at  TIMESTAMPTZ,
    skipped_in_row INTEGER     NOT NULL DEFAULT 0 CHECK (skipped_in_row >= 0),
    created_at     TIMESTAMPTZ NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL,
    CONSTRAINT schedules_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT schedules_tenant_id_source_id_key UNIQUE (tenant_id, source_id),
    CONSTRAINT schedules_source_fkey FOREIGN KEY (tenant_id, source_id) REFERENCES sources (tenant_id, id),
    CONSTRAINT schedules_next_run_check CHECK (enabled = (next_run_at IS NOT NULL)),
    CONSTRAINT schedules_catch_up_check CHECK (enabled OR catch_up_at IS NULL)
);
-- The scheduler's scan across tenants (ADR 0013, "Системный доступ").
CREATE INDEX schedules_next_run_at_idx ON schedules (next_run_at) WHERE enabled;
CREATE INDEX schedules_catch_up_at_idx ON schedules (catch_up_at) WHERE catch_up_at IS NOT NULL;

ALTER TABLE runs ADD COLUMN schedule_id UUID;
ALTER TABLE runs ADD CONSTRAINT runs_schedule_fkey
    FOREIGN KEY (tenant_id, schedule_id) REFERENCES schedules (tenant_id, id);
ALTER TABLE runs ADD CONSTRAINT runs_schedule_check
    CHECK (schedule_id IS NULL OR trigger IN ('schedule', 'catch_up'));

-- The journal of fires: kind is what fired (a cron moment or the catch-up), outcome what came of it.
-- run_id is the run created, or for skipped_active the run that was active. A downtime is one row:
-- missed_count fires from scheduled_for through missed_until. alert marks the fire whose skip made
-- skipped_in_row reach the alert threshold.
CREATE TABLE schedule_fires (
    id             UUID        NOT NULL PRIMARY KEY,
    tenant_id      UUID        NOT NULL REFERENCES tenants (id),
    schedule_id    UUID        NOT NULL,
    kind           TEXT        NOT NULL CHECK (kind IN ('schedule', 'catch_up')),
    scheduled_for  TIMESTAMPTZ NOT NULL,
    outcome        TEXT        NOT NULL
        CHECK (outcome IN ('run_created', 'skipped_active', 'skipped_gone', 'refused', 'skipped_downtime')),
    run_id         UUID,
    reason         TEXT CHECK (reason IN ('source_deleted', 'agent_revoked', 'unknown_plugin', 'unknown_repository')),
    missed_count   INTEGER CHECK (missed_count >= 1),
    missed_until   TIMESTAMPTZ,
    skipped_in_row INTEGER     NOT NULL CHECK (skipped_in_row >= 0),
    alert          BOOLEAN     NOT NULL,
    recorded_at    TIMESTAMPTZ NOT NULL,
    CONSTRAINT schedule_fires_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT schedule_fires_schedule_fkey FOREIGN KEY (tenant_id, schedule_id) REFERENCES schedules (tenant_id, id),
    CONSTRAINT schedule_fires_run_fkey FOREIGN KEY (tenant_id, run_id) REFERENCES runs (tenant_id, id),
    CONSTRAINT schedule_fires_run_check CHECK ((outcome IN ('run_created', 'skipped_active')) = (run_id IS NOT NULL)),
    CONSTRAINT schedule_fires_reason_outcome_check CHECK ((outcome IN ('skipped_gone', 'refused')) = (reason IS NOT NULL)),
    CONSTRAINT schedule_fires_downtime_check CHECK (
        (outcome = 'skipped_downtime') = (missed_count IS NOT NULL)
        AND (missed_count IS NULL) = (missed_until IS NULL)
        AND (outcome <> 'skipped_downtime' OR kind = 'schedule')
    )
);
-- Each fire at most once, held by the database: a second attempt at the same moment fails.
CREATE UNIQUE INDEX schedule_fires_once_key ON schedule_fires (tenant_id, schedule_id, kind, scheduled_for)
    WHERE outcome <> 'skipped_downtime';
CREATE INDEX schedule_fires_journal_idx ON schedule_fires (tenant_id, schedule_id, recorded_at DESC, id DESC);
