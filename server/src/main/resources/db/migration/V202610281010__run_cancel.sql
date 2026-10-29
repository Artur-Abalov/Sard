-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- J1a: run cancel. Cancelling is a mark on a run in dispatched or running, not a status (the status and
-- the D6 index do not change): who asked and when, and how the cancel ended. A step cancelled in the
-- queue was never dispatched; a step the server closed as cancelled keeps the late status its agent
-- reports afterwards.

ALTER TABLE runs
    ADD COLUMN cancel_requested_at TIMESTAMPTZ,
    ADD COLUMN cancel_requested_by TEXT,
    ADD COLUMN cancel_result       TEXT CHECK (cancel_result IN ('pending', 'cancelled', 'unconfirmed', 'finished_first')),
    ADD CONSTRAINT runs_cancel_check CHECK (
        (cancel_requested_at IS NULL) = (cancel_requested_by IS NULL)
        AND (cancel_requested_at IS NULL) = (cancel_result IS NULL));

ALTER TABLE run_steps
    ADD COLUMN late_status TEXT CHECK (late_status IN ('succeeded', 'failed', 'cancelled', 'timed_out', 'rejected', 'lost'));

-- A step cancelled before it was sent has no dispatched_at.
ALTER TABLE run_steps DROP CONSTRAINT run_steps_dispatched_check;
ALTER TABLE run_steps ADD CONSTRAINT run_steps_dispatched_check
    CHECK ((status = 'queued') = (dispatched_at IS NULL) OR (status = 'cancelled' AND dispatched_at IS NULL));
