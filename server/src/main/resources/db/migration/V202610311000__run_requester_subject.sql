-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- CORE1 Р26: the actor of every event of a run is the subject the SubjectSource named when the run was created, also
-- when the run is moved later on a thread that has no subject (the agent's stream). requested_by keeps its meaning
-- (who started a manual run, for the readers of the API); these two columns hold the subject behind it. NULL for a
-- schedule, a verification and a run created while the source named nobody.
ALTER TABLE runs
    ADD COLUMN requested_by_kind TEXT,
    ADD COLUMN requested_by_id   TEXT,
    ADD CONSTRAINT runs_requested_by_subject_both
        CHECK ((requested_by_kind IS NULL) = (requested_by_id IS NULL));

UPDATE runs SET requested_by_kind = 'administrator', requested_by_id = 'admin' WHERE requested_by IS NOT NULL;
