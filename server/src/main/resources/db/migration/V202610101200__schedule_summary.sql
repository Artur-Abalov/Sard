-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- F3b: what a schedule tells after the fact. notify_on_success asks for a message about every
-- successful scheduled run (failures and recoveries are always told). missed_count_capped marks
-- a downtime whose count stopped at the limit. catch_up_fire_id ties the downtimes a catch-up
-- stands for to the fire of that catch-up: every downtime recorded before the catch-up fire and
-- not yet claimed by an earlier one (an exact link).

ALTER TABLE schedules ADD COLUMN notify_on_success BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE schedule_fires ADD COLUMN missed_count_capped BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE schedule_fires ADD COLUMN catch_up_fire_id UUID;
ALTER TABLE schedule_fires ADD CONSTRAINT schedule_fires_catch_up_fire_fkey
    FOREIGN KEY (tenant_id, catch_up_fire_id) REFERENCES schedule_fires (tenant_id, id);
ALTER TABLE schedule_fires ADD CONSTRAINT schedule_fires_downtime_only_check
    CHECK (outcome = 'skipped_downtime' OR (NOT missed_count_capped AND catch_up_fire_id IS NULL));

-- When the owed catch-up became owed (Р15): a change of the schedule cancels a pending catch-up and leaves its
-- downtimes to nobody; the next catch-up stands only for the downtimes recorded since it became owed.
-- F3a kept only the time of the last change, which clears a pending catch-up: a pending one is owed since then.
ALTER TABLE schedules ADD COLUMN catch_up_owed_since TIMESTAMPTZ;
UPDATE schedules SET catch_up_owed_since = updated_at WHERE catch_up_at IS NOT NULL;

-- Downtimes of F3a belong to the first catch-up fire recorded after them (within a tick the catch-up is written
-- first, so a downtime of the very same moment is later than it and belongs to the next catch-up), unless the last change of the
-- schedule came between and cancelled that catch-up: the only cancellation F3a data still shows.
UPDATE schedule_fires d
SET catch_up_fire_id = (
    SELECT c.id FROM schedule_fires c
    JOIN schedules s ON s.tenant_id = c.tenant_id AND s.id = c.schedule_id
    WHERE c.tenant_id = d.tenant_id AND c.schedule_id = d.schedule_id AND c.kind = 'catch_up'
      AND c.recorded_at > d.recorded_at
      AND (d.recorded_at >= s.updated_at OR c.recorded_at < s.updated_at)
    ORDER BY c.recorded_at, c.id LIMIT 1)
WHERE d.outcome = 'skipped_downtime';
