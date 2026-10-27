-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- V5: what a verifier reports about its sandbox besides the runtime (V202610191300): the runtime's version and the
-- sandbox images it has, [{"name", "version", "digest"}]. From Register and from a heartbeat that carries them.
ALTER TABLE agents ADD COLUMN verify_sandbox_version TEXT;
ALTER TABLE agents ADD COLUMN verify_images JSONB NOT NULL DEFAULT '[]';
