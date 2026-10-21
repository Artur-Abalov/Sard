-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- V3: whether a repository has a recovery key, as the agent reported it in its last Register (ADR 0008: only the
-- flag, never a key, a key id or a count). 'unknown' is an agent older than V3 or an agent that could not read the
-- keys; it is never read as 'absent'.

ALTER TABLE agent_repositories
    ADD COLUMN recovery_key TEXT NOT NULL DEFAULT 'unknown' CHECK (recovery_key IN ('present', 'absent', 'unknown'));
