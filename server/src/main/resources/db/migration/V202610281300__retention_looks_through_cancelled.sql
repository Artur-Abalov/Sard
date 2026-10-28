-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- OQ-264 (a defect of J1f, V202610281200): a record with the status 'cancelled' proves nothing, so it is looked
-- through here as everywhere else in J1f. Before, a cancel made after an old failure counted as the "next not passed"
-- record and the stages of that failure were cleared although no new outcome had been reached. Now only a record that
-- is not cancelled can make an older record of its kind not the last one. The rest is as in V202610211300.

CREATE OR REPLACE FUNCTION clear_verification_evidence(p_tenant UUID, p_cutoff TIMESTAMPTZ, p_limit INTEGER) RETURNS INTEGER
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
                        AND n.status <> 'cancelled'
                        AND (n.status = 'passed') = (v.status = 'passed')
                        AND (n.verified_at, n.id) > (v.verified_at, v.id))
        ORDER BY v.verified_at, v.id LIMIT p_limit FOR UPDATE SKIP LOCKED)
    UPDATE sard.restore_verifications r SET stages = '[]'::jsonb, mismatches = '[]'::jsonb
    FROM picked WHERE r.tenant_id = p_tenant AND r.id = picked.id;
    GET DIAGNOSTICS cleared = ROW_COUNT;
    RETURN cleared;
END
$$;
