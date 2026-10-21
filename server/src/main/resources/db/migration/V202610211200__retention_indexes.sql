-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- H1 phase 2 (docs/specs/server/server-retention.feature): the cleaning of what is older than its term asks, for each
-- row it may delete, whether anything still refers to it, and picks the old rows by their mark. The foreign keys
-- below had no index on the referring side (a run could not be deleted without a scan of the referring table), and
-- the marks the terms count from had none that starts with the tenant.

-- Who refers to a run, a step or a fire: the cleaning asks before it deletes.
CREATE INDEX verification_jobs_run_idx ON verification_jobs (tenant_id, run_id) WHERE run_id IS NOT NULL;
CREATE INDEX restore_verifications_step_idx ON restore_verifications (tenant_id, step_id) WHERE step_id IS NOT NULL;
CREATE INDEX schedule_fires_run_idx ON schedule_fires (tenant_id, run_id) WHERE run_id IS NOT NULL;
CREATE INDEX schedule_fires_catch_up_idx ON schedule_fires (tenant_id, catch_up_fire_id) WHERE catch_up_fire_id IS NOT NULL;
CREATE INDEX overdue_episodes_closed_by_run_idx ON overdue_episodes (tenant_id, closed_by_run_id)
    WHERE closed_by_run_id IS NOT NULL;
CREATE INDEX verification_stale_episodes_closed_by_idx ON verification_stale_episodes (tenant_id, closed_by_id)
    WHERE closed_by_id IS NOT NULL;

-- The old rows by their marks. A delivery that is still pending is never deleted, so it is not in the index.
CREATE INDEX notification_deliveries_retention_idx ON notification_deliveries (tenant_id, created_at)
    WHERE status <> 'pending';
CREATE INDEX schedule_fires_recorded_at_idx ON schedule_fires (tenant_id, recorded_at);
CREATE INDEX restore_verifications_verified_at_idx ON restore_verifications (tenant_id, verified_at);
