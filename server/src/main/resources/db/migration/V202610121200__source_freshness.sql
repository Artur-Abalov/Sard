-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- F6: freshness of scheduled sources (ADR 00XX-draft-source-freshness). A source whose schedule is
-- on and that has not succeeded for one and a half of the schedule's interval is overdue: an episode
-- opens, and the first success after it closes it. Notifications tell of either end, not only of runs.

-- One row per episode. counted_from is the success it counts from (NULL: the source never succeeded);
-- deadline is when it became overdue; closed_by_run_id is the success that ended it, NULL when the
-- episode ended because the source lost its schedule or was deleted. At most one open per source.
CREATE TABLE overdue_episodes (
    id               UUID        NOT NULL PRIMARY KEY,
    tenant_id        UUID        NOT NULL REFERENCES tenants (id),
    source_id        UUID        NOT NULL,
    counted_from     TIMESTAMPTZ,
    deadline         TIMESTAMPTZ NOT NULL,
    opened_at        TIMESTAMPTZ NOT NULL,
    closed_at        TIMESTAMPTZ,
    closed_by_run_id UUID,
    CONSTRAINT overdue_episodes_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT overdue_episodes_source_fkey FOREIGN KEY (tenant_id, source_id) REFERENCES sources (tenant_id, id),
    CONSTRAINT overdue_episodes_run_fkey FOREIGN KEY (tenant_id, closed_by_run_id) REFERENCES runs (tenant_id, id),
    CONSTRAINT overdue_episodes_closed_check CHECK (closed_at IS NOT NULL OR closed_by_run_id IS NULL)
);
CREATE UNIQUE INDEX overdue_episodes_open_key ON overdue_episodes (tenant_id, source_id) WHERE closed_at IS NULL;

-- A delivery tells of a subject: a finished run, an episode that opened (overdue) or one a success
-- closed (overdue_over). The run's key stays; an episode's is (episode, subject, channel).
ALTER TABLE notification_deliveries ALTER COLUMN run_id DROP NOT NULL;
ALTER TABLE notification_deliveries ADD COLUMN subject TEXT NOT NULL DEFAULT 'run'
    CONSTRAINT notification_deliveries_subject_check CHECK (subject IN ('run', 'overdue', 'overdue_over'));
ALTER TABLE notification_deliveries ALTER COLUMN subject DROP DEFAULT;
ALTER TABLE notification_deliveries ADD COLUMN episode_id UUID;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_subject_id_check
    CHECK ((subject = 'run') = (run_id IS NOT NULL) AND (subject = 'run') = (episode_id IS NULL));
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_episode_fkey
    FOREIGN KEY (tenant_id, episode_id) REFERENCES overdue_episodes (tenant_id, id);
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_episode_channel_key
    UNIQUE (tenant_id, episode_id, subject, channel);

-- The planner looks for episodes opened or closed within the delivery time to live.
CREATE INDEX overdue_episodes_opened_at_idx ON overdue_episodes (opened_at);
CREATE INDEX overdue_episodes_closed_at_idx ON overdue_episodes (closed_at) WHERE closed_by_run_id IS NOT NULL;
