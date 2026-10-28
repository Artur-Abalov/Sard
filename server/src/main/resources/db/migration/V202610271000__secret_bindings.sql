-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- S11: every secret of an agent host with its binding to a plugin and its targets, from Register:
-- [{"name", "plugin", "targets", "auth", "allow_tls", "allow_no_channel_binding", "problem"}]. Names and targets only:
-- never a value, never a path. The console warns about a source its agent's binding does not allow.
ALTER TABLE agents ADD COLUMN secret_bindings JSONB NOT NULL DEFAULT '[]';
