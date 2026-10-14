-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- F4b (docs/specs/server/onboarding-self-backup.feature): the self-backup step and the key copy of the wizard.
--
-- The address of the storage the wizard built the command for, without credentials (OQ-203), and when the owner
-- confirmed that the repository password and the storage access are kept outside the server (OQ-206): both belong to
-- the binding, so a new binding asks for the confirmation again.
ALTER TABLE self_backups ADD COLUMN location TEXT CHECK (location IS NULL OR length(location) <= 1024);
ALTER TABLE self_backups ADD COLUMN keys_confirmed_at TIMESTAMPTZ;

-- Marks of the installation as a whole, set once and never taken back (Р21). "upgraded": the installation already
-- had a live agent other than the built-in one, or a source of the administrator, when it first started with F4b.
-- Only the new agents are held back there, not the work it already does (OQ-201).
CREATE TABLE onboarding_marks (
    mark      TEXT        PRIMARY KEY CHECK (mark IN ('upgraded')),
    marked_at TIMESTAMPTZ NOT NULL
);

INSERT INTO onboarding_marks (mark, marked_at)
SELECT 'upgraded', now()
WHERE EXISTS (SELECT 1 FROM agents WHERE builtin = false AND revoked_at IS NULL)
   OR EXISTS (SELECT 1 FROM sources WHERE system_role IS NULL AND deleted_at IS NULL);
