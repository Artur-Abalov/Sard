-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- FXs (t3-defects Д1, Д2): when a dispatched or running step is lost unless its agent reports it.
-- Set when the agent's session ends, at server start and by a Hello that does not list the step;
-- cleared by a Hello that lists it, by progress, a result or a send. Kept here, not in memory,
-- so that a server restart or an agent that never returns cannot leave a step active forever.
ALTER TABLE run_steps ADD COLUMN lost_deadline TIMESTAMPTZ;

-- The periodic check: steps in flight whose deadline has come, across tenants.
CREATE INDEX run_steps_lost_deadline_idx ON run_steps (lost_deadline)
    WHERE status IN ('dispatched', 'running') AND lost_deadline IS NOT NULL;
