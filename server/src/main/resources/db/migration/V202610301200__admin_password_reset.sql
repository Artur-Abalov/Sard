-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- CORE1 Р32 (docs/specs/server/server-core-extensions.feature): the mark an admin-reset leaves for the next start.
--
-- The command admin-reset is a process of its own with no listeners, so it cannot publish the settings.changed event
-- of the reset. In the statement that deletes the administrator's hash it writes this row; the server, once its
-- listeners are ready, publishes one admin_password event for the row and deletes it, so a restart publishes nothing
-- more. One row per tenant: several resets before a start leave one.
CREATE TABLE admin_password_resets (
    tenant_id UUID        PRIMARY KEY REFERENCES tenants (id),
    reset_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
