-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- V6: restore verification in the console and in notifications (docs/specs/server/verification-console.feature,
-- verification-notifications.feature, docs/adr/00XX-draft-verification-notifications.md).

-- The threshold «not verified successfully for longer than N days» is set from the console beside the cycle: one
-- for the installation. NULL until the first save that carries it; sard.verification.stale-after applies until then.
ALTER TABLE verification_schedule ADD COLUMN stale_after_days SMALLINT CHECK (stale_after_days BETWEEN 1 AND 90);

-- The threshold and the zone of the cycle in force when an episode opened: its notification names them, whatever the
-- console sets later. The episodes that exist were opened under the default of V4, seven days, in UTC.
ALTER TABLE verification_stale_episodes ADD COLUMN threshold_days INTEGER;
ALTER TABLE verification_stale_episodes ADD COLUMN timezone TEXT;
UPDATE verification_stale_episodes SET threshold_days = 7, timezone = 'UTC';
ALTER TABLE verification_stale_episodes ALTER COLUMN threshold_days SET NOT NULL;
ALTER TABLE verification_stale_episodes ALTER COLUMN timezone SET NOT NULL;
-- The defaults are what the V4 server implied (seven days, UTC), so that it can still open an episode on a migrated
-- database, as after a rollback of the binary alone; the V6 server always names both.
ALTER TABLE verification_stale_episodes ALTER COLUMN threshold_days SET DEFAULT 7;
ALTER TABLE verification_stale_episodes ALTER COLUMN timezone SET DEFAULT 'UTC';
ALTER TABLE verification_stale_episodes
    ADD CONSTRAINT verification_stale_episodes_threshold_check CHECK (threshold_days BETWEEN 1 AND 90);

-- The installation is in the «no verifier» state (V6, ОВ4): sources to verify and not one live verifier. An episode
-- opens when the state begins and closes when it ends; the notification tells of the beginning only. `sources` is
-- how many sources were to be verified then. At most one open episode per tenant: a second server opening one at the
-- same moment changes nothing.
CREATE TABLE no_verifier_episodes (
    id         UUID        NOT NULL PRIMARY KEY,
    tenant_id  UUID        NOT NULL REFERENCES tenants (id),
    sources    INTEGER     NOT NULL CHECK (sources >= 1),
    opened_at  TIMESTAMPTZ NOT NULL,
    closed_at  TIMESTAMPTZ,
    CONSTRAINT no_verifier_episodes_tenant_id_id_key UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX no_verifier_episodes_open_key ON no_verifier_episodes (tenant_id) WHERE closed_at IS NULL;
CREATE INDEX no_verifier_episodes_opened_at_idx ON no_verifier_episodes (opened_at);

-- The notification queue learns three more subjects (the ADR): a verification record, which tells a failure or a pass
-- after failures or after silence; a stale episode, whose opening is told; the «no verifier» episode. Exactly one
-- subject per delivery, as before.
ALTER TABLE notification_deliveries ADD COLUMN verification_id UUID;
ALTER TABLE notification_deliveries ADD COLUMN stale_episode_id UUID;
ALTER TABLE notification_deliveries ADD COLUMN no_verifier_episode_id UUID;
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_subject_check;
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_subject_check
    CHECK (num_nonnulls(run_id, fire_id, episode_id, certificate_event_id, verification_id, stale_episode_id,
                        no_verifier_episode_id) = 1
        AND (episode_id IS NULL) = (episode_event IS NULL));
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_verification_fkey
    FOREIGN KEY (tenant_id, verification_id) REFERENCES restore_verifications (tenant_id, id);
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_verification_channel_key
    UNIQUE (tenant_id, verification_id, channel);
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_stale_episode_fkey
    FOREIGN KEY (tenant_id, stale_episode_id) REFERENCES verification_stale_episodes (tenant_id, id);
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_stale_episode_channel_key
    UNIQUE (tenant_id, stale_episode_id, channel);
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_no_verifier_episode_fkey
    FOREIGN KEY (tenant_id, no_verifier_episode_id) REFERENCES no_verifier_episodes (tenant_id, id);
ALTER TABLE notification_deliveries ADD CONSTRAINT notification_deliveries_no_verifier_episode_channel_key
    UNIQUE (tenant_id, no_verifier_episode_id, channel);

-- The planner looks for records and stale episodes made within the delivery time to live.
CREATE INDEX restore_verifications_recent_idx ON restore_verifications (verified_at);
CREATE INDEX verification_stale_episodes_opened_at_idx ON verification_stale_episodes (opened_at);
