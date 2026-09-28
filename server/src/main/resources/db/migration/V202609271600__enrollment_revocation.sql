-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- S2b: revocation and the optional operator-facing label of an enrollment token
-- (docs/specs/server/agent-enrollment.feature, decisions 2 and 6).

ALTER TABLE enrollment_tokens
    ADD COLUMN revoked_at TIMESTAMPTZ,
    -- Empty means no label (decision 2); NULL is never written, so the check is unconditional.
    ADD COLUMN label TEXT NOT NULL DEFAULT '' CONSTRAINT enrollment_tokens_label_check CHECK (char_length(label) <= 200),
    -- Priority used > revoked > expired > active (decision 9): a token is never both used and
    -- revoked — Enroll and revoke each claim the token exclusively.
    ADD CONSTRAINT enrollment_tokens_revocation_check CHECK (used_at IS NULL OR revoked_at IS NULL);

ALTER TABLE enrollment_tokens ALTER COLUMN label DROP DEFAULT;
