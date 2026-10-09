-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- F3b (ADR 0054): the alert about fires skipped in a row goes through the same queue as run
-- notifications. A delivery is about a finished run or about a schedule fire that raised an alert,
-- never both: the retry, lease, time to live and metrics of the queue stay one piece of code.

ALTER TABLE notification_deliveries ALTER COLUMN run_id DROP NOT NULL;
ALTER TABLE notification_deliveries ADD COLUMN fire_id UUID;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_fire_fkey
    FOREIGN KEY (tenant_id, fire_id) REFERENCES schedule_fires (tenant_id, id);
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_fire_channel_key
    UNIQUE (tenant_id, fire_id, channel);
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_subject_check
    CHECK ((run_id IS NULL) <> (fire_id IS NULL));

-- The planner looks for alerts recorded within the time to live.
CREATE INDEX schedule_fires_alert_idx ON schedule_fires (recorded_at) WHERE alert;
