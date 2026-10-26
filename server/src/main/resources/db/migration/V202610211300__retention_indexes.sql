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

-- DB2 (docs/adr/0069-database-roles.md): the records of verifications are kept for good and are append-only for the
-- server. sard_app may read them and add one (INSERT, from the default privileges), never change or remove one, and
-- not even empty the table: UPDATE, DELETE and TRUNCATE are taken back. What the verifier found (stages and
-- mismatches) is cleared after sard.retention.verifications, by the function below, which is owned by sard_owner and
-- refuses a term shorter than the floor kept in the database. A floor is fixed by a migration: sard_app may read it.
REVOKE UPDATE, DELETE, TRUNCATE ON restore_verifications FROM sard_app;

CREATE TABLE retention_floors (
    name     TEXT PRIMARY KEY,
    min_days INTEGER NOT NULL CHECK (min_days > 0)
);
INSERT INTO retention_floors (name, min_days) VALUES ('verification_evidence', 180);
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON retention_floors FROM sard_app;

-- Clears the evidence of at most p_limit records of verifications of one tenant that were made before p_cutoff, except
-- the last passed and the last not passed one of each source, and returns how many it cleared. A cutoff later than
-- now minus the floor is taken as now minus the floor: a caller cannot clear what is younger, whatever it asks. The
-- records themselves, their status, level, time and key holders, are not touched.
CREATE FUNCTION clear_verification_evidence(p_tenant UUID, p_cutoff TIMESTAMPTZ, p_limit INTEGER) RETURNS INTEGER
    LANGUAGE plpgsql
    SECURITY DEFINER
    SET search_path = pg_catalog, sard, pg_temp
AS $$
DECLARE
    floor_days INTEGER;
    cutoff TIMESTAMPTZ;
    cleared INTEGER;
BEGIN
    SELECT min_days INTO STRICT floor_days FROM sard.retention_floors WHERE name = 'verification_evidence';
    cutoff := LEAST(p_cutoff, now() - make_interval(days => floor_days));
    WITH picked AS (
        SELECT v.id FROM sard.restore_verifications v
        WHERE v.tenant_id = p_tenant AND v.verified_at < cutoff
          AND (v.stages <> '[]'::jsonb OR v.mismatches <> '[]'::jsonb)
          AND EXISTS (SELECT 1 FROM sard.restore_verifications n
                      WHERE n.tenant_id = v.tenant_id AND n.source_id = v.source_id
                        AND (n.status = 'passed') = (v.status = 'passed')
                        AND (n.verified_at, n.id) > (v.verified_at, v.id))
        ORDER BY v.verified_at, v.id LIMIT p_limit FOR UPDATE SKIP LOCKED)
    UPDATE sard.restore_verifications r SET stages = '[]'::jsonb, mismatches = '[]'::jsonb
    FROM picked WHERE r.tenant_id = p_tenant AND r.id = picked.id;
    GET DIAGNOSTICS cleared = ROW_COUNT;
    RETURN cleared;
END
$$;
