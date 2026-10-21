// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.BeforeAll
import org.junit.jupiter.api.extension.RegisterExtension
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * R2b, the restore of a PostgreSQL database from the console with real containers
 * (docs/specs/server/console-restore.feature, rules "Восстановление PostgreSQL" and "Сквозные отказы PostgreSQL";
 * docs/specs/agent/console-restore.feature): PostgreSQL 18, the agent with its pg_restore 18, a backup started
 * through the REST API, then the restore of its snapshot into a new database by the same API. The names of the
 * tests are the names of the scenarios.
 */
class ConsoleRestorePostgresTest {
    @Test
    fun `Бэкап PostgreSQL восстанавливается из консоли в новую базу`() {
        val (snapshot, sourceId) = backedUp("restore-ok")

        val started = Backups.restore(sard, sourceId, snapshot, agent.agent.agentId, restoreConfig("app_restored"))
        val step = Backups.awaitFinished(sard, started.stepId, RESTORE_TIMEOUT)

        assertEquals("succeeded", step.status, step.message)
        assertEquals("succeeded", Backups.runStatus(sard, started.runId))
        assertEquals("app_restored", Backups.outputField(sard, started.stepId, "database"))
        assertEquals("2", Backups.outputField(sard, started.stepId, "tablesRestored"))
        assertEquals(TABLES.map { pg.query(it, "app") }, TABLES.map { pg.query(it, "app_restored") }, "the tables or the rows differ")
        assertEquals("1", pg.query("select count(*) from pg_database where datname = 'app_restored'", "postgres"))
        assertTrue(agent.agent.host.tree("${AgentHost.STATE_DIR}/executor/restore-journal").isEmpty(), "the rollback journal stays after the result")
    }

    @Test
    fun `Существующая база с тем же именем — отказ до изменений`() {
        val (snapshot, sourceId) = backedUp("restore-exists")
        pg.sql("create database taken")
        pg.sql("create table keep(x int)", "insert into keep values (7)", database = "taken")

        val started = Backups.restore(sard, sourceId, snapshot, agent.agent.agentId, restoreConfig("taken"))
        val step = Backups.awaitFinished(sard, started.stepId, RESTORE_TIMEOUT)

        assertEquals("failed", step.status)
        assertTrue(step.message.orEmpty().startsWith("DATABASE_EXISTS (check)"), step.message)
        assertTrue("nothing was restored" in step.message.orEmpty(), step.message)
        assertEquals("keep|7", pg.query("select 'keep' || '|' || x from keep", "taken"))
        assertEquals("1", pg.query("select count(*) from pg_tables where schemaname = 'public'", "taken"))
        assertNull(Backups.outputField(sard, started.stepId, "database"))
    }

    @Test
    fun `Целевой PostgreSQL 14, снимок с сервера 18`() {
        val (snapshot, sourceId) = backedUp("restore-old")
        val old = PgServer(sard, 14, alias = "db14").start()
        old.sql("create role restorer login createdb password ${'$'}${'$'}$PG_PASSWORD${'$'}${'$'}")

        val started =
            Backups.restore(sard, sourceId, snapshot, agent.agent.agentId, restoreConfig("app_on_old", host = "db14"))
        val step = Backups.awaitFinished(sard, started.stepId, RESTORE_TIMEOUT)

        assertEquals("failed", step.status)
        val message = step.message.orEmpty()
        assertTrue(message.startsWith("PG_SERVER_TOO_OLD (check)"), message)
        assertTrue("14." in message && "18." in message, "the message does not name both versions: $message")
        assertEquals("0", old.query("select count(*) from pg_database where datname = 'app_on_old'", "postgres"))
    }

    @Test
    fun `Ошибка pg_restore удаляет созданную базу`() {
        pg.sql("create database ext")
        // dblink is not a trusted extension: a role without superuser cannot create it, so pg_restore of the dump fails.
        pg.sql("create extension dblink", "create table t(x int)", "insert into t values (1)", database = "ext")
        val started = agent.backup("restore-ext-${UUID.randomUUID()}", PgAgent.configK(database = "ext"))
        val backup = agent.finish(started)
        assertEquals("succeeded", backup.status, backup.message)

        val restore =
            Backups.restore(sard, started.sourceId, checkNotNull(backup.snapshotId), agent.agent.agentId, restoreConfig("ext_restored"))
        val step = Backups.awaitFinished(sard, restore.stepId, RESTORE_TIMEOUT)

        assertEquals("failed", step.status)
        val message = step.message.orEmpty()
        assertTrue(message.startsWith("PG_RESTORE_FAILED (restore)"), message)
        assertTrue(message.endsWith("nothing was restored"), message)
        assertFalse("\n" in message, "the message is not one line")
        assertEquals("0", pg.query("select count(*) from pg_database where datname = 'ext_restored'", "postgres"))
        assertNull(Backups.outputField(sard, restore.stepId, "leftover"))
    }

    /** The database `app` backed up through the console; the restic id of its snapshot and the source. */
    private fun backedUp(name: String): Pair<String, UUID> {
        val started = agent.backup("$name-${UUID.randomUUID()}", PgAgent.configK())
        val step = agent.finish(started)
        assertEquals("succeeded", step.status, step.message)
        return checkNotNull(step.snapshotId) to started.sourceId
    }

    /** The restore config of the schema of the agent: the role `restorer` with CREATEDB, the password of the secret `pg-app`. */
    private fun restoreConfig(
        database: String,
        host: String = "db",
    ): String = """{"host":"$host","user":"restorer","password_ref":"pg-app","tls_mode":"disable","database":"$database"}"""

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val RESTORE_TIMEOUT = java.time.Duration.ofMinutes(3)

        /** What the tables of the seeded database hold: the count and a checksum of the values. */
        private val TABLES =
            listOf(
                "select count(*) || ' ' || md5(string_agg(v, '' order by id)) from t",
                "select count(*) || ' ' || md5(string_agg(note, '' order by id)) from u",
            )

        private lateinit var pg: PgServer
        private lateinit var agent: PgAgent

        @JvmStatic
        @BeforeAll
        fun start() {
            pg = PgServer(sard, 18).start()
            pg.seed()
            pg.sql("create role restorer login createdb password ${'$'}${'$'}$PG_PASSWORD${'$'}${'$'}")
            agent = PgAgent(sard, 18, "pg18-restore")
            assertNotNull(Backups.repositoryId(sard, agent.agent.agentId, PgAgent.REPOSITORY))
        }
    }
}
