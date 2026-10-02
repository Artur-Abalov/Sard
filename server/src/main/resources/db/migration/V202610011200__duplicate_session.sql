-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- S8b: when the stream manager last confirmed a duplicate session of the agent (ADR 0026, rule 2:
-- a refused second stream whose holder then proved alive; the S5b mark). The latest confirmation
-- only; never cleared by the server.
ALTER TABLE agents ADD COLUMN duplicate_session_at TIMESTAMPTZ;
