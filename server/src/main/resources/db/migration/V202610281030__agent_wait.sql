-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- J1c: the wait deadline of a run whose agent is offline. A run waits in the queue until agent_wait_until (set
-- when it is created: the source's next schedule fire, at most sard.run.agent-wait.max; a manual run and a
-- restore wait the maximum). When it passes and the agent is still offline the run and its step end as
-- agent_unavailable: not run, not a failure; agent_unavailable_since keeps the agent's last contact then.

ALTER TABLE runs DROP CONSTRAINT runs_status_check;
ALTER TABLE runs ADD CONSTRAINT runs_status_check
    CHECK (status IN ('queued', 'dispatched', 'running', 'succeeded', 'failed', 'cancelled', 'agent_unavailable'));

ALTER TABLE run_steps DROP CONSTRAINT run_steps_status_check;
ALTER TABLE run_steps ADD CONSTRAINT run_steps_status_check
    CHECK (status IN ('queued', 'dispatched', 'running', 'succeeded', 'failed', 'cancelled', 'timed_out',
                      'rejected', 'lost', 'agent_unavailable'));

-- A step that was never sent has no dispatched_at: cancelled in the queue, or expired there.
ALTER TABLE run_steps DROP CONSTRAINT run_steps_dispatched_check;
ALTER TABLE run_steps ADD CONSTRAINT run_steps_dispatched_check
    CHECK ((status = 'queued') = (dispatched_at IS NULL)
        OR (status IN ('cancelled', 'agent_unavailable') AND dispatched_at IS NULL));

-- NULL: the run does not wait for an agent (a verification, and records before J1c).
ALTER TABLE runs
    ADD COLUMN agent_wait_until        TIMESTAMPTZ,
    ADD COLUMN agent_unavailable_since TIMESTAMPTZ,
    ADD CONSTRAINT runs_agent_unavailable_since_check
        CHECK (agent_unavailable_since IS NULL OR status = 'agent_unavailable');

-- The tick that closes expired waits reads the queued runs with a deadline.
CREATE INDEX runs_queued_agent_wait_idx ON runs (agent_wait_until)
    WHERE status = 'queued' AND agent_wait_until IS NOT NULL;
