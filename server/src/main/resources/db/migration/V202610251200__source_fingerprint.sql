-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- V2: the fingerprint of the source a backup brought (canonical bytes and their SHA-256, the snapshot's tag
-- sard.fp) and restic's number of files, kept with the snapshot. A snapshot made before V2, or by an agent or a
-- plugin that takes no fingerprint, has neither: a verification checks it up to level 3 (V4, V5). The
-- verification job copies both from here (V4).

ALTER TABLE snapshots
    ADD COLUMN fingerprint        BYTEA,
    ADD COLUMN fingerprint_sha256 TEXT CHECK (fingerprint_sha256 ~ '^[0-9a-f]{64}$'),
    ADD COLUMN total_files        BIGINT CHECK (total_files >= 0),
    ADD CONSTRAINT snapshots_fingerprint_whole CHECK ((fingerprint IS NULL) = (fingerprint_sha256 IS NULL)),
    ADD CONSTRAINT snapshots_fingerprint_size CHECK (octet_length(fingerprint) <= 1048576);
