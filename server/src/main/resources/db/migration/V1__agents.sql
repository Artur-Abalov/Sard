-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov

CREATE TABLE agents (
    id            UUID PRIMARY KEY,
    hostname      TEXT        NOT NULL,
    agent_version TEXT        NOT NULL,
    registered_at TIMESTAMPTZ NOT NULL,
    last_seen_at  TIMESTAMPTZ
);
