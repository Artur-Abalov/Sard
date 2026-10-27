-- SPDX-License-Identifier: AGPL-3.0-only
-- Copyright 2026 Artur Abalov
--
-- Prepares a PostgreSQL database for Sard (DB2, docs/adr/0069-database-roles.md): the roles, the schema "sard" and
-- who owns and may use what. The embedded installation runs it as the db-init service over the local socket; the
-- operator of an external cluster runs it once with psql as a superuser, in the database Sard will use:
--
--     psql -d sard -f bootstrap.sql
--
-- It sets no password. Roles with LOGIN get theirs from the one who runs it (db-init writes them into volumes; an
-- external DBA chooses them and gives the server only sard_app's, the migrate command only sard_migrator's).
-- It is repeatable: a second run changes nothing, and after a restore by a superuser (pg_restore --no-owner
-- --no-acl) it gives the objects back to sard_owner and the roles their privileges again.
--
-- The roles (D26):
--   sard_owner     no LOGIN; owns the schema and everything in it
--   sard_migrator  LOGIN; member of sard_owner, becomes it for the migrations (SET ROLE), never for anything else
--   sard_app       LOGIN; the server: reads and writes rows, nothing more
--   sard_backup    LOGIN, read-only; the agent next to the server dumps the database with it
--   sard_monitor   LOGIN without a password; pg_monitor, for a monitoring system of the operator's
--
-- Needs a superuser. A role with CREATEROLE but without SUPERUSER works too if it owns the database, has the ADMIN
-- option on pg_read_all_data and pg_monitor, and the Sard roles do not exist yet or it has the ADMIN option on them
-- (it has it on the ones it creates). Two things are then up to a superuser, once per database, before the first run, and
-- the script fails (changing nothing) if they are not done:
--   * PUBLIC must no longer have EXECUTE on the large-object functions (server-side file read and write); the error
--     lists the functions and the REVOKE to run;
--   * sard_app must be a member of no role the caller cannot revoke it from (the script revokes every membership of
--     sard_app); the error names the role and the REVOKE to run.
-- Any other role is refused (docs/operator/16-postgresql.md).
-- This is the one psql meta-command of the file: the script stops at the first error, and as it is one transaction,
-- nothing is left half done.
\set ON_ERROR_STOP on

BEGIN;
-- A second run says nothing about what it found in place.
SET LOCAL client_min_messages = warning;

DO $bootstrap$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = current_user AND (rolsuper OR rolcreaterole)) THEN
        RAISE EXCEPTION 'bootstrap.sql needs a role with SUPERUSER or CREATEROLE (the latter must own the database and have the ADMIN option on pg_read_all_data and pg_monitor); % has neither (docs/operator/16-postgresql.md)',
            current_user;
    END IF;
END
$bootstrap$;

-- Before anything is created: a caller who is not a superuser cannot take EXECUTE on the large-object functions
-- from PUBLIC (they belong to the bootstrap superuser; the privilege is kept per database), so while PUBLIC holds it, the script stops.
DO $bootstrap$
DECLARE
    open_to_public text;
BEGIN
    IF (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN
        RETURN;
    END IF;
    SELECT string_agg(f.signature, ', ' ORDER BY f.signature) INTO open_to_public
    FROM (VALUES ('lo_create(oid)'), ('lo_creat(integer)'), ('lo_from_bytea(oid, bytea)'), ('lo_import(text)'),
                 ('lo_import(text, oid)')) AS f (signature)
    WHERE has_function_privilege('public', f.signature::regprocedure, 'EXECUTE');
    IF open_to_public IS NOT NULL THEN
        RAISE EXCEPTION '%', concat_ws(E'\n',
            'PUBLIC can still execute ' || open_to_public || ' in database ' || current_database() || '.',
            'Any database user could then read and write files of the database server through large objects, and a non-superuser cannot take that right away.',
            'A superuser must run this once in database ' || current_database() || ' (connected to it, or to template1 before the database is created), then run bootstrap.sql again:',
            '    REVOKE EXECUTE ON FUNCTION lo_create(oid), lo_creat(integer), lo_from_bytea(oid, bytea), lo_import(text), lo_import(text, oid) FROM PUBLIC;',
            '(docs/operator/16-postgresql.md)');
    END IF;
END
$bootstrap$;

-- The roles, with the attributes of D26 set on every run, so a role changed by hand is brought back. Only what
-- differs is altered: PostgreSQL refuses to a caller without SUPERUSER even a NOSUPERUSER (and the like) that
-- changes nothing.
DO $bootstrap$
DECLARE
    spec record;
    changes text;
BEGIN
    FOR spec IN
        SELECT * FROM (VALUES
            ('sard_owner', false),
            ('sard_migrator', true),
            ('sard_app', true),
            ('sard_backup', true),
            ('sard_monitor', true)) AS r (name, login)
    LOOP
        IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = spec.name) THEN
            EXECUTE format('CREATE ROLE %I', spec.name);
        END IF;
        SELECT concat_ws(' ',
                   CASE WHEN rolcanlogin <> spec.login THEN CASE WHEN spec.login THEN 'LOGIN' ELSE 'NOLOGIN' END END,
                   CASE WHEN rolsuper THEN 'NOSUPERUSER' END,
                   CASE WHEN rolcreatedb THEN 'NOCREATEDB' END,
                   CASE WHEN rolcreaterole THEN 'NOCREATEROLE' END,
                   CASE WHEN rolreplication THEN 'NOREPLICATION' END,
                   CASE WHEN rolbypassrls THEN 'NOBYPASSRLS' END)
          INTO changes FROM pg_roles WHERE rolname = spec.name;
        IF changes <> '' THEN
            EXECUTE format('ALTER ROLE %I %s', spec.name, changes);
        END IF;
    END LOOP;
END
$bootstrap$;

-- Someone who is not a superuser must be a member of sard_owner to give it the schema and the default
-- privileges; the membership lasts until the end of this script.
DO $bootstrap$
BEGIN
    IF NOT (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN
        EXECUTE format('GRANT sard_owner TO %I', current_user);
    END IF;
END
$bootstrap$;

-- The dump role takes up to four sessions and starts every one read-only.
ALTER ROLE sard_backup CONNECTION LIMIT 4;
ALTER ROLE sard_backup SET default_transaction_read_only = on;
-- Every session of a Sard role starts in the schema "sard", wherever it is made.
ALTER ROLE sard_migrator SET search_path = sard;
ALTER ROLE sard_app SET search_path = sard;
ALTER ROLE sard_backup SET search_path = sard;

-- Membership. The migrations run as sard_owner through SET ROLE and never inherit its rights otherwise;
-- sard_app is a member of nothing at all: whatever it was made a member of (in a cluster that has been used before)
-- is revoked, and if the caller cannot, the script stops and names the role.
DO $bootstrap$
DECLARE
    granted record;
BEGIN
    -- Since PostgreSQL 16 a REVOKE removes only the grants made by the revoking role: every membership goes
    -- with its own grantor.
    FOR granted IN SELECT roleid::regrole::text AS role, grantor::regrole::text AS grantor
                   FROM pg_auth_members WHERE member = 'sard_app'::regrole
    LOOP
        BEGIN
            EXECUTE format('REVOKE %s FROM sard_app GRANTED BY %s', granted.role, granted.grantor);
        EXCEPTION WHEN insufficient_privilege THEN
            NULL;
        END;
    END LOOP;
    FOR granted IN SELECT roleid::regrole::text AS role, grantor::regrole::text AS grantor
                   FROM pg_auth_members WHERE member = 'sard_app'::regrole
    LOOP
        RAISE EXCEPTION 'sard_app is a member of % (granted by %), and % cannot revoke it: a superuser must run REVOKE % FROM sard_app GRANTED BY %, then bootstrap.sql again (docs/operator/16-postgresql.md)',
            granted.role, granted.grantor, current_user, granted.role, granted.grantor;
    END LOOP;
END
$bootstrap$;
GRANT sard_owner TO sard_migrator WITH INHERIT FALSE, SET TRUE;
GRANT pg_read_all_data TO sard_backup;
GRANT pg_monitor TO sard_monitor;

-- The database: the Sard roles connect, and nobody but its owner makes temporary tables (the one way to write
-- left to a role that may only read). PUBLIC loses what it was granted by default and could write with.
DO $bootstrap$
BEGIN
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO sard_migrator, sard_app, sard_backup, sard_monitor',
                   current_database());
    EXECUTE format('REVOKE TEMPORARY ON DATABASE %I FROM PUBLIC', current_database());
END
$bootstrap$;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
-- The large-object functions belong to the bootstrap superuser of the cluster: only a superuser may take their
-- rights from PUBLIC. For any other caller PUBLIC has lost them already (checked at the top).
DO $bootstrap$
BEGIN
    IF (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN
        REVOKE EXECUTE ON FUNCTION lo_create(oid), lo_creat(integer), lo_from_bytea(oid, bytea),
            lo_import(text), lo_import(text, oid) FROM PUBLIC;
    END IF;
END
$bootstrap$;

-- The schema.
CREATE SCHEMA IF NOT EXISTS sard AUTHORIZATION sard_owner;
DO $bootstrap$
BEGIN
    IF (SELECT nspowner FROM pg_namespace WHERE nspname = 'sard') <> 'sard_owner'::regrole THEN
        ALTER SCHEMA sard OWNER TO sard_owner;
    END IF;
END
$bootstrap$;
REVOKE ALL ON SCHEMA sard FROM PUBLIC;
GRANT USAGE ON SCHEMA sard TO sard_app;

-- What sard_owner creates from now on: rows to read and write for sard_app, functions for it to call and for
-- nobody else. No GRANT is needed in a migration. The tables that must not be written freely (the append-only
-- ones, REVOKEd by their migrations) are the exception, not the rule.
ALTER DEFAULT PRIVILEGES FOR ROLE sard_owner IN SCHEMA sard
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO sard_app;
ALTER DEFAULT PRIVILEGES FOR ROLE sard_owner IN SCHEMA sard
    GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO sard_app;
ALTER DEFAULT PRIVILEGES FOR ROLE sard_owner IN SCHEMA sard
    GRANT EXECUTE ON FUNCTIONS TO sard_app;
ALTER DEFAULT PRIVILEGES FOR ROLE sard_owner REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;

-- A database that someone else deployed (pg_restore --no-owner --no-acl by a superuser, the recovery of F7):
-- its objects go to sard_owner and get the privileges a migration would have given them. Nothing of this touches
-- an installation whose objects are right: an owner that is sard_owner already and an ACL that exists stay.
DO $bootstrap$
DECLARE
    obj record;
    -- Written by sard_app only by INSERT (append-only), and only read (a limit fixed by migrations, the histories
    -- of migrations, which a compromised server must not forge).
    append_only constant text[] := ARRAY['restore_verifications'];
    read_only constant text[] := ARRAY['retention_floors', 'flyway_schema_history', 'flyway_schema_history_extensions'];
BEGIN
    FOR obj IN
        SELECT c.oid::regclass::text AS name,
               CASE c.relkind WHEN 'S' THEN 'SEQUENCE' WHEN 'v' THEN 'VIEW' WHEN 'm' THEN 'MATERIALIZED VIEW'
                              WHEN 'f' THEN 'FOREIGN TABLE' ELSE 'TABLE' END AS kind
        FROM pg_class c
        WHERE c.relnamespace = 'sard'::regnamespace AND c.relowner <> 'sard_owner'::regrole
          AND c.relkind IN ('r', 'p', 'S', 'v', 'm', 'f')
          -- A sequence that belongs to a column follows its table.
          AND (c.relkind <> 'S' OR NOT EXISTS (SELECT FROM pg_depend d WHERE d.objid = c.oid
                                               AND d.deptype IN ('a', 'i') AND d.refclassid = 'pg_class'::regclass))
    LOOP
        EXECUTE format('ALTER %s %s OWNER TO sard_owner', obj.kind, obj.name);
    END LOOP;
    FOR obj IN
        SELECT p.oid::regprocedure::text AS name, CASE p.prokind WHEN 'p' THEN 'PROCEDURE' ELSE 'FUNCTION' END AS kind
        FROM pg_proc p
        WHERE p.pronamespace = 'sard'::regnamespace AND p.proowner <> 'sard_owner'::regrole
    LOOP
        EXECUTE format('ALTER %s %s OWNER TO sard_owner', obj.kind, obj.name);
    END LOOP;
    FOR obj IN
        SELECT t.oid::regtype::text AS name, CASE t.typtype WHEN 'd' THEN 'DOMAIN' ELSE 'TYPE' END AS kind
        FROM pg_type t
        WHERE t.typnamespace = 'sard'::regnamespace AND t.typowner <> 'sard_owner'::regrole
          AND t.typtype IN ('e', 'd') AND t.typelem = 0
    LOOP
        EXECUTE format('ALTER %s %s OWNER TO sard_owner', obj.kind, obj.name);
    END LOOP;

    FOR obj IN
        SELECT c.oid::regclass::text AS name, c.relname AS short
        FROM pg_class c
        WHERE c.relnamespace = 'sard'::regnamespace AND c.relkind IN ('r', 'p') AND c.relacl IS NULL
    LOOP
        IF obj.short = ANY (read_only) THEN
            EXECUTE format('GRANT SELECT ON %s TO sard_app', obj.name);
        ELSIF obj.short = ANY (append_only) THEN
            EXECUTE format('GRANT SELECT, INSERT ON %s TO sard_app', obj.name);
        ELSE
            EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %s TO sard_app', obj.name);
        END IF;
    END LOOP;
    FOR obj IN
        SELECT c.oid::regclass::text AS name FROM pg_class c
        WHERE c.relnamespace = 'sard'::regnamespace AND c.relkind = 'S' AND c.relacl IS NULL
    LOOP
        EXECUTE format('GRANT USAGE, SELECT, UPDATE ON SEQUENCE %s TO sard_app', obj.name);
    END LOOP;
    FOR obj IN
        SELECT p.oid::regprocedure::text AS name, CASE p.prokind WHEN 'p' THEN 'PROCEDURE' ELSE 'FUNCTION' END AS kind
        FROM pg_proc p WHERE p.pronamespace = 'sard'::regnamespace AND p.proacl IS NULL
    LOOP
        EXECUTE format('REVOKE EXECUTE ON %s %s FROM PUBLIC', obj.kind, obj.name);
        EXECUTE format('GRANT EXECUTE ON %s %s TO sard_app', obj.kind, obj.name);
    END LOOP;
END
$bootstrap$;

DO $bootstrap$
BEGIN
    IF NOT (SELECT rolsuper FROM pg_roles WHERE rolname = current_user) THEN
        EXECUTE format('REVOKE sard_owner FROM %I', current_user);
    END IF;
END
$bootstrap$;

COMMIT;
