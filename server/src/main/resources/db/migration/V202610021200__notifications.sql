-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- S9a: deliveries of run notifications (OQ-047). The durable fact is the finished run that S7a
-- commits; a background planner turns each finished run into one row per configured channel,
-- and a sender works the rows off with retries. Nothing here is written by the transaction
-- that finishes a run, so a notification can never roll back or delay it.

-- One notification of one run through one channel; (tenant_id, run_id, channel) is the
-- idempotency key. channel is a pattern, not a list: channels come from configuration and
-- extensions (Telegram now, email or a webhook later).
CREATE TABLE notification_deliveries (
    id              UUID        NOT NULL PRIMARY KEY,
    tenant_id       UUID        NOT NULL REFERENCES tenants (id),
    run_id          UUID        NOT NULL,
    channel         TEXT        NOT NULL CHECK (channel ~ '^[a-z][a-z0-9-]{0,31}$'),
    status          TEXT        NOT NULL CHECK (status IN ('pending', 'delivered', 'skipped', 'failed', 'expired')),
    -- Attempts that count toward the limit: 5xx, network and timeouts; a 429 does not count.
    attempts        INTEGER     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    -- When the sender may pick the row up; a claim moves it by the lease.
    next_attempt_at TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    finished_at     TIMESTAMPTZ,
    -- Why the last attempt did not deliver: an HTTP status and the channel's description, never a credential.
    last_error      TEXT CHECK (char_length(last_error) <= 500),
    CONSTRAINT notification_deliveries_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT notification_deliveries_run_channel_key UNIQUE (tenant_id, run_id, channel),
    CONSTRAINT notification_deliveries_run_fkey FOREIGN KEY (tenant_id, run_id) REFERENCES runs (tenant_id, id),
    CONSTRAINT notification_deliveries_finished_check CHECK ((status = 'pending') = (finished_at IS NULL))
);

CREATE INDEX notification_deliveries_due_idx ON notification_deliveries (next_attempt_at)
    WHERE status = 'pending';

-- The planner looks for runs finished within the delivery time to live.
CREATE INDEX runs_finished_at_idx ON runs (finished_at) WHERE finished_at IS NOT NULL;
