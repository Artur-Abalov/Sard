-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- J1b: step timeouts. A source may name its own timeout for backups and for restores (minutes, at most a day);
-- without one the tenant's defaults apply (run_defaults, 6 h and 12 h until set). The timeout a step was
-- created with is kept in the step (timeout_seconds): later edits change no run in flight. timed_out_by says who
-- closed a timed out step: the agent (its own deadline) or the server (the agent stayed silent past the
-- timeout and the lost window).

ALTER TABLE sources
    ADD COLUMN backup_timeout_minutes  INTEGER CHECK (backup_timeout_minutes BETWEEN 1 AND 1440),
    ADD COLUMN restore_timeout_minutes INTEGER CHECK (restore_timeout_minutes BETWEEN 1 AND 1440);

-- NULL: a step without a timeout (a verification, whose timeout is its attempt's, and records before J1b).
ALTER TABLE run_steps
    ADD COLUMN timeout_seconds BIGINT CHECK (timeout_seconds > 0),
    ADD COLUMN timed_out_by    TEXT CHECK (timed_out_by IN ('agent', 'server')),
    ADD CONSTRAINT run_steps_timed_out_by_status_check CHECK (timed_out_by IS NULL OR status = 'timed_out');

-- The server's own check of silent steps reads the running ones with a timeout.
CREATE INDEX run_steps_running_timeout_idx ON run_steps (started_at)
    WHERE status = 'running' AND timeout_seconds IS NOT NULL AND lost_deadline IS NULL;

-- The default timeouts of a tenant (ДJ8); without a row 360 and 720 minutes apply.
CREATE TABLE run_defaults (
    tenant_id              UUID        NOT NULL PRIMARY KEY REFERENCES tenants (id),
    backup_timeout_minutes  INTEGER    NOT NULL CHECK (backup_timeout_minutes BETWEEN 1 AND 1440),
    restore_timeout_minutes INTEGER    NOT NULL CHECK (restore_timeout_minutes BETWEEN 1 AND 1440),
    updated_at             TIMESTAMPTZ NOT NULL
);
