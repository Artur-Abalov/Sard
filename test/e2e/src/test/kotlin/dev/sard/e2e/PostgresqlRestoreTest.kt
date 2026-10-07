// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

/**
 * F1, the restore scenarios of docs/specs/agent/postgresql-plugin.feature: a database is backed up by
 * the real agent and restored by hand, as docs/plugins/postgresql.md says, into a clean PostgreSQL of the
 * same major version (14 and 18, F1 ПГ19): first the global objects through psql, then the dump through
 * createdb and pg_restore. Names of the tests are the names of the scenarios.
 */
class PostgresqlRestoreTest {
    @Test
    fun `Бэкап и восстановление в чистый PostgreSQL дают те же данные и роли, версия 14`() = restoresTheSame(14)

    @Test
    fun `Бэкап и восстановление в чистый PostgreSQL дают те же данные и роли, версия 18`() = restoresTheSame(18)

    @Test
    fun `С паролями ролей восстановленная роль входит прежним паролем`() {
        val stand = Stand.of(18)
        stand.source.sql("alter role backup superuser")
        val restored = stand.backupAndRestore(stand.config("\"globals_role_passwords\":true"))
        assertEquals(0, restored.logsIn("app_rw", "qa-rw-pass").exitCode, "app_rw cannot sign in with its old password")
        assertTrue("SCRAM-SHA-256" in restored.globals, "the global objects hold no password hash")
    }

    @Test
    fun `Без паролей ролей восстановленная роль входа по паролю не имеет, версия 14`() = withoutPasswords(14)

    @Test
    fun `Без паролей ролей восстановленная роль входа по паролю не имеет, версия 18`() = withoutPasswords(18)

    @Test
    fun `Исключённые схема и таблица не восстанавливаются`() {
        val stand = Stand.of(18)
        stand.source.sql("create schema audit", "create table audit.a(x int)", "create table public.log_x(x int)", database = "app")
        val config = stand.configK("\"exclude_schemas\":[\"audit\"]", "\"exclude_tables\":[\"public.log_x\"]")
        val step = stand.agent.finish(stand.agent.backup("excluded-${UUID.randomUUID()}", config))
        assertEquals("succeeded", step.status, step.message)
        val target = stand.target
        target.put("/tmp/app.dump", stand.agent.restored(checkNotNull(step.snapshotId), "/app.dump"))
        target.sql("create database restored")
        // The roles are not restored here (include_globals is off): owners and rights are left out.
        val restore = target.exec("pg_restore", "-U", "postgres", "--no-owner", "--no-acl", "-d", "restored", "/tmp/app.dump")
        assertEquals(0, restore.exitCode, restore.stderr)
        assertEquals("0", target.query("select count(*) from pg_namespace where nspname = 'audit'", "restored"))
        assertEquals("0", target.query("select count(*) from pg_tables where tablename = 'log_x'", "restored"))
        assertEquals(
            stand.source.query("select count(*) || ' ' || md5(string_agg(v, '' order by id)) from t", "app"),
            target.query("select count(*) || ' ' || md5(string_agg(v, '' order by id)) from t", "restored"),
        )
    }

    private fun restoresTheSame(major: Int) {
        val stand = Stand.of(major)
        val restored = stand.backupAndRestore(stand.config())
        assertEquals(stand.source.fingerprint("app"), stand.target.fingerprint("restored"))
        assertEquals("app_owner", stand.target.query("select tableowner from pg_tables where tablename = 't'", "restored"))
        assertFalse("SCRAM-SHA-256" in restored.globals, "the global objects hold a password hash")
    }

    private fun withoutPasswords(major: Int) {
        val stand = Stand.of(major)
        val restored = stand.backupAndRestore(stand.config())
        assertEquals("1", stand.target.query("select count(*) from pg_roles where rolname = 'app_rw'", "postgres"))
        assertNotEquals(0, restored.logsIn("app_rw", "qa-rw-pass").exitCode, "app_rw signs in without a password in the dump")
        assertFalse("SCRAM-SHA-256" in restored.globals, "the global objects hold a password hash")
    }

    /** A source cluster with the seeded database `app`, the agent on it and a clean target cluster of the same major version. */
    private class Stand(
        val source: PgServer,
        val agent: PgAgent,
        val target: PgServer,
    ) {
        /** The config K of the spec for the source cluster, with the global objects on (KG) or off ([configK]). */
        fun config(vararg extra: String): String = PgAgent.config(*extra, host = source.alias)

        fun configK(vararg extra: String): String = PgAgent.configK(*extra, host = source.alias)

        /** What the global objects snapshot held, and the sign-in of a role into the restored cluster. */
        inner class Restored(
            val globals: String,
        ) {
            /** psql of the agent's container, over the network: the clusters accept a password there. */
            fun logsIn(
                role: String,
                password: String,
            ) = agent.container.execInContainer("env", "PGPASSWORD=$password", "psql", "-h", target.alias, "-U", role, "-d", "postgres", "-Atc", "select 1")
        }

        /**
         * Backs up `app` with [config] and restores both snapshots into the target the way the documentation
         * says: the globals through psql, then createdb and pg_restore of the dump into `restored`.
         */
        fun backupAndRestore(config: String): Restored {
            val step = agent.finish(agent.backup("restore-${UUID.randomUUID()}", config))
            assertEquals("succeeded", step.status, step.message)
            val main = checkNotNull(step.snapshotId)
            val globalsSnapshot = agent.globalsOf(main)
            assertTrue("postgresql.part=globals" in agent.tags(globalsSnapshot))
            val globals = agent.restored(globalsSnapshot, "/app.globals.sql")
            target.put("/tmp/app.globals.sql", globals)
            // Errors "role ... already exists" are expected for the roles the new cluster has already.
            val psql = target.exec("psql", "-U", "postgres", "-X", "-d", "postgres", "-f", "/tmp/app.globals.sql")
            val unexpected = psql.stderr.lines().filter { "ERROR" in it && "already exists" !in it }
            assertEquals(emptyList(), unexpected, "psql of the global objects")
            target.put("/tmp/app.dump", agent.restored(main, "/app.dump"))
            target.sql("create database restored")
            val restore = target.exec("pg_restore", "-U", "postgres", "-d", "restored", "/tmp/app.dump")
            assertEquals(0, restore.exitCode, restore.stderr)
            return Restored(String(globals))
        }

        companion object {
            /** Containers of every stand have names of their own: a class runs several stands in one environment. */
            fun of(major: Int): Stand {
                val id = UUID.randomUUID().toString().take(8)
                val source = PgServer(sard, major, alias = "db-$id").start().also { it.seed() }
                val agent = PgAgent(sard, major, "restore$major-$id")
                val target = PgServer(sard, major, alias = "db-restore-$id").start()
                return Stand(source, agent, target)
            }
        }
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()
    }
}
