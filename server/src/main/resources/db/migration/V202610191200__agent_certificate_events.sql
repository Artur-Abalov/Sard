-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- C1 phase 3 (docs/specs/server/agent-certificate-expiry.feature, П36): the periodic check records, once per
-- certificate, that it reached 30, 7 or 1 of 365 of its lifetime left, or expired. The notification of the event
-- goes through the same queue as run notifications (S9a): a delivery tells of a run, a schedule fire, an overdue
-- episode or a certificate event, exactly one of them.

-- One row per fact. (tenant_id, serial, event) is the idempotency key: two servers recording the same fact, or one
-- check repeated, write one row. A threshold that a check finds passed together with a smaller one is not recorded.
CREATE TABLE agent_certificate_events (
    id          UUID        NOT NULL PRIMARY KEY,
    tenant_id   UUID        NOT NULL REFERENCES tenants (id),
    serial      TEXT        NOT NULL,
    agent_id    UUID        NOT NULL,
    event       TEXT        NOT NULL CHECK (event IN ('threshold-30', 'threshold-7', 'threshold-1', 'expired')),
    recorded_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT agent_certificate_events_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT agent_certificate_events_serial_event_key UNIQUE (tenant_id, serial, event),
    CONSTRAINT agent_certificate_events_certificate_fkey FOREIGN KEY (serial, tenant_id, agent_id)
        REFERENCES agent_certificates (serial, tenant_id, agent_id)
);

ALTER TABLE notification_deliveries ADD COLUMN certificate_event_id UUID;
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_subject_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_subject_check
    CHECK (num_nonnulls(run_id, fire_id, episode_id, certificate_event_id) = 1
        AND (episode_id IS NULL) = (episode_event IS NULL));
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_certificate_event_fkey
    FOREIGN KEY (tenant_id, certificate_event_id) REFERENCES agent_certificate_events (tenant_id, id);
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_certificate_event_channel_key
    UNIQUE (tenant_id, certificate_event_id, channel);

-- The planner looks for events recorded within the delivery time to live.
CREATE INDEX agent_certificate_events_recorded_at_idx ON agent_certificate_events (recorded_at);
