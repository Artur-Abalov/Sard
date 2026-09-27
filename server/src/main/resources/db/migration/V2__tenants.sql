-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- Multi-tenancy baseline (ADR 0013). The open core has exactly one tenant;
-- its id is TenantResolver.DEFAULT_TENANT_ID and must never change.

CREATE TABLE tenants (
    id         UUID PRIMARY KEY,
    name       TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO tenants (id, name) VALUES ('00000000-0000-0000-0000-000000000001', 'default');

-- The default only backfills existing rows; new rows get their tenant from Hibernate.
ALTER TABLE agents
    ADD COLUMN tenant_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001' REFERENCES tenants (id);
ALTER TABLE agents ALTER COLUMN tenant_id DROP DEFAULT;

-- Target of composite foreign keys (tenant_id, agent_id); also the tenant_id index.
ALTER TABLE agents ADD CONSTRAINT agents_tenant_id_id_key UNIQUE (tenant_id, id);
