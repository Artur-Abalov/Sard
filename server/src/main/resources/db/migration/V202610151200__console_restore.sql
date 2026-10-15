-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- R2a: restore from the console. What an agent announces for it (the restore directory of the host and the
-- schema of the restore config of a plugin, ADR 0008: names and metadata only) and the restore as a kind of
-- run next to the backup.

-- NULL for an agent that has not announced one (before R2, or before its first Register).
ALTER TABLE agents
    ADD COLUMN restore_dir TEXT CHECK (restore_dir ~ '^/');

-- NULL: the plugin cannot be restored from the console.
ALTER TABLE agent_plugins
    ADD COLUMN restore_config_schema JSONB;

-- A restore is a run of its own kind. restore holds what the console shows of it (snapshot, repository,
-- config, target); requested_by is who started a manual run, NULL for a schedule.
ALTER TABLE runs
    ADD COLUMN kind         TEXT NOT NULL DEFAULT 'backup' CHECK (kind IN ('backup', 'restore')),
    ADD COLUMN requested_by TEXT,
    ADD COLUMN restore      JSONB,
    ADD CONSTRAINT runs_restore_check CHECK ((kind = 'restore') = (restore IS NOT NULL));

UPDATE runs SET requested_by = 'admin' WHERE trigger = 'manual';

-- D6 holds for backups only: a restore neither blocks a backup of its source nor is blocked by one.
DROP INDEX runs_active_source_key;
CREATE UNIQUE INDEX runs_active_source_key ON runs (tenant_id, source_id)
    WHERE kind = 'backup' AND status IN ('queued', 'dispatched', 'running');
