-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov

-- The sample extension's table: every event of the core its listener receives, one row each.
create table sample_events (
    id          bigint generated always as identity primary key,
    type        text        not null,
    detail      text        not null,
    recorded_at timestamptz not null default now()
);
