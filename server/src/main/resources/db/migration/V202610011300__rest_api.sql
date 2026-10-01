-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- S8b: what the REST API shows beyond what S6a and S7a stored.

-- Files processed and expected in the current phase (StepProgress.files_processed and files_total,
-- OQ-049); NULL while the agent has reported none, or does not know the total.
ALTER TABLE run_steps
    ADD COLUMN files_processed BIGINT CHECK (files_processed >= 0),
    ADD COLUMN files_total     BIGINT CHECK (files_total >= 0);

-- A snapshot of a step that did not succeed but produced one (S8b В8: a failed backup, e.g. with
-- unreadable files, still saved a snapshot restic knows); the console marks it as incomplete.
ALTER TABLE snapshots ADD COLUMN partial BOOLEAN NOT NULL DEFAULT false;
