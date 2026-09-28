-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- S3: revoking an agent refuses every certificate it holds (ADR 0013, "Целевая схема":
-- one certificate is revoked in agent_certificates, the whole agent here).
ALTER TABLE agents ADD COLUMN revoked_at TIMESTAMPTZ;
