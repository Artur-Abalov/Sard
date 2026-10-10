-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- F4a (docs/specs/server/onboarding-setup.feature): the state of the first start.
--
-- onboarding_steps is installation-wide, not per tenant: it holds one row per step that is done. The admin step has
-- no row of its own: it is done exactly when the default tenant has an administrator.
CREATE TABLE onboarding_steps (
    step         TEXT        PRIMARY KEY CHECK (step IN ('ca', 'self_backup', 'keys_confirmed')),
    completed_at TIMESTAMPTZ NOT NULL
);

-- The administrator of a tenant: only the Argon2id hash (a PHC string) of the password, never the password.
-- Deleting the row (admin-reset) opens the admin step again.
CREATE TABLE administrators (
    tenant_id           UUID        PRIMARY KEY REFERENCES tenants (id),
    password_hash       TEXT        NOT NULL CHECK (password_hash <> ''),
    password_changed_at TIMESTAMPTZ NOT NULL
);

-- Where the server's CA came from, by the fingerprint of its root (Р12, Р19). The row is written before the CA appears
-- in the CA directory, so a CA that is there has one; a CA without a row stops the start (CA_ORIGIN_NOT_RECORDED).
CREATE TABLE ca_origins (
    fingerprint TEXT        PRIMARY KEY CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    origin      TEXT        NOT NULL CHECK (origin IN ('generated', 'imported')),
    recorded_at TIMESTAMPTZ NOT NULL
);
