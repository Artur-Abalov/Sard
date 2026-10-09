-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- F5 (D14): sard_self, the role the agent next to the server (sard-self,
-- docs/operations/self-agent.md) dumps this database with. It reads every table
-- (pg_read_all_data, PostgreSQL 14+) and writes nothing: no privilege on any
-- table or sequence, no temporary tables, no large objects. Its sessions also
-- start read-only, but that default is the session's to change, so the
-- privileges are what holds.
--
-- Creating a role and granting pg_read_all_data take a superuser, which the
-- server's user is in deploy/docker-compose.yml. With any other user this
-- migration only warns; the operator runs the same statements by hand.
-- The role gets no password here: it logs in only after one is set.
DO $$
BEGIN
    IF NOT (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN
        RAISE WARNING 'sard_self not set up: % is not a superuser (docs/operations/self-agent.md)', current_user;
        RETURN;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'sard_self') THEN
        CREATE ROLE sard_self LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS
            CONNECTION LIMIT 4;
    END IF;
    GRANT pg_read_all_data TO sard_self;
    ALTER ROLE sard_self SET default_transaction_read_only = on;
    -- Granted to PUBLIC by default: the role's only ways to write left after the above.
    -- The server's own user is the database owner and a superuser, so it keeps them.
    EXECUTE format('REVOKE TEMPORARY ON DATABASE %I FROM PUBLIC', current_database());
    REVOKE CREATE ON SCHEMA public FROM PUBLIC;
    REVOKE EXECUTE ON FUNCTION lo_create(oid), lo_creat(integer), lo_from_bytea(oid, bytea),
        lo_import(text), lo_import(text, oid) FROM PUBLIC;
END
$$;
