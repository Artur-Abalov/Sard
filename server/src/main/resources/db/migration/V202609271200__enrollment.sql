-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- Agent enrollment (S2a): one-time tokens and the certificates issued for them.
-- Versions are timestamps from here on, so parallel branches do not collide.

-- An enrollment token is stored as SHA-256 of its 32-byte secret, never as text
-- (docs/specs/enrollment-token.md). The hash is unique across tenants: Enroll
-- finds the token before it knows the tenant (ADR 0013, "Целевая схема").
CREATE TABLE enrollment_tokens (
    id         UUID        PRIMARY KEY,
    tenant_id  UUID        NOT NULL REFERENCES tenants (id),
    token_hash BYTEA       NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    agent_id   UUID,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT enrollment_tokens_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT enrollment_tokens_agent_fkey FOREIGN KEY (tenant_id, agent_id) REFERENCES agents (tenant_id, id),
    CONSTRAINT enrollment_tokens_expiry_check CHECK (expires_at > created_at),
    -- Only a used token names an agent; Enroll claims the token before the agent exists.
    CONSTRAINT enrollment_tokens_use_check CHECK (agent_id IS NULL OR used_at IS NOT NULL)
);

-- The serial is 128 random bits (ADR 0014) as 32 lower-case hex digits; it is
-- global because the S3 interceptor looks a certificate up during the handshake.
CREATE TABLE agent_certificates (
    serial     TEXT        PRIMARY KEY CHECK (serial ~ '^[0-9a-f]{32}$'),
    tenant_id  UUID        NOT NULL REFERENCES tenants (id),
    agent_id   UUID        NOT NULL,
    issued_at  TIMESTAMPTZ NOT NULL,
    not_after  TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    CONSTRAINT agent_certificates_agent_fkey FOREIGN KEY (tenant_id, agent_id) REFERENCES agents (tenant_id, id),
    CONSTRAINT agent_certificates_validity_check CHECK (not_after > issued_at)
);

CREATE INDEX agent_certificates_tenant_id_agent_id_idx ON agent_certificates (tenant_id, agent_id);
