-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- PL7-2 (SARD-15): why a partial snapshot is incomplete, as the server decided it when it recorded the step's output:
-- unreadable_files, snapshot_set_incomplete or unknown. The count and the first paths of unreadable files live in the
-- step's output (run_steps.output, JSON). A snapshot recorded before this says unknown: no agent told the number.
ALTER TABLE snapshots ADD COLUMN incomplete_reason TEXT;
UPDATE snapshots SET incomplete_reason = 'unknown' WHERE partial;
ALTER TABLE snapshots ADD CONSTRAINT snapshots_incomplete_reason_check
    CHECK (incomplete_reason IN ('unreadable_files', 'snapshot_set_incomplete', 'unknown'));
