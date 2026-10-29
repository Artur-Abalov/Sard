-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- J1f: an administrator cancels a restore verification job (docs/specs/server/run-cancel-deadlines.feature,
-- docs/adr/00XX-draft-run-cancel-deadlines.md). The job carries the same mark as a run: who asked, when, and how the
-- cancel ended. A cancelled job closes with a record whose status is 'cancelled'; it carries who and when, needs no
-- verifier or step (a job cancelled in the queue never had an attempt) and never recovers anything.

ALTER TABLE verification_jobs
    ADD COLUMN cancel_requested_at TIMESTAMPTZ,
    ADD COLUMN cancel_requested_by TEXT,
    ADD COLUMN cancel_result       TEXT CHECK (cancel_result IN ('pending', 'cancelled', 'unconfirmed', 'finished_first')),
    ADD CONSTRAINT verification_jobs_cancel_check CHECK (
        (cancel_requested_at IS NULL) = (cancel_requested_by IS NULL)
        AND (cancel_requested_at IS NULL) = (cancel_result IS NULL));

ALTER TABLE restore_verifications
    ADD COLUMN cancelled_by TEXT,
    ADD COLUMN cancelled_at TIMESTAMPTZ;

ALTER TABLE restore_verifications DROP CONSTRAINT restore_verifications_status_check;
ALTER TABLE restore_verifications ADD CONSTRAINT restore_verifications_status_check
    CHECK (status IN ('passed', 'failed', 'cannot_verify', 'infrastructure_failure', 'no_verifier', 'cancelled'));

ALTER TABLE restore_verifications DROP CONSTRAINT restore_verifications_verdict_check;
ALTER TABLE restore_verifications ADD CONSTRAINT restore_verifications_verdict_check
    CHECK (status IN ('infrastructure_failure', 'no_verifier', 'cancelled')
        OR (step_id IS NOT NULL AND verifier_agent_id IS NOT NULL));

ALTER TABLE restore_verifications ADD CONSTRAINT restore_verifications_cancelled_check
    CHECK ((status = 'cancelled') = (cancelled_by IS NOT NULL AND cancelled_at IS NOT NULL)
        AND (cancelled_by IS NULL) = (cancelled_at IS NULL));
