// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.BeforeAll
import org.junit.jupiter.api.extension.RegisterExtension
import org.testcontainers.images.builder.Transferable
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * F1, the scenarios tagged `@e2e` of docs/specs/agent/postgresql-plugin.feature (names of the tests are
 * the names of the scenarios), PostgreSQL 18 and the agent with its pg_dump 18 (F1 ПГ20): a real server,
 * the real agent and restic, a step started through the REST API.
 *
 * Not here: the scenario that cancels a step or lets its timeout run out while it uploads. The server
 * neither cancels a run (no REST call, no `CancelStep` is ever sent) nor gives a step a timeout, so the
 * REST API cannot do either; the agent's side is tested by `plugins/postgresql` (`TestCancelAndTimeout…`).
 */
class PostgresqlSourceTest {
    @Test
    fun `Сервер не сохраняет источник postgresql со ссылкой на неизвестный секрет`() {
        val body =
            """{"name":"nope-${UUID.randomUUID()}","agentId":"${agent.agent.agentId}","plugin":"postgresql",""" +
                """"repositoryName":"main","config":${PgAgent.config("\"include_globals\":false").replace("\"pg-app\"", "\"nope\"")}}"""
        val answer = SardApi(sard).post("/api/v1/sources", body)
        assertEquals(422, answer.status)
        assertTrue("config/password_ref" in answer.body, "the error is not at config/password_ref: ${answer.body}")
        assertTrue("nope" in answer.body, "the error does not name the secret: ${answer.body}")
    }

    @Test
    fun `Ошибка подключения или входа проваливает шаг с причиной PostgreSQL, пароль в секрете не подходит`() {
        agent.setPassword("wrong")
        try {
            failsWith("password authentication failed for user", PgAgent.configK())
        } finally {
            agent.setPassword(PG_PASSWORD)
        }
    }

    @Test
    fun `Ошибка подключения или входа проваливает шаг с причиной PostgreSQL, базы app на сервере нет`() {
        failsWith("database \"nope\" does not exist", PgAgent.configK(database = "nope"))
    }

    @Test
    fun `Ошибка подключения или входа проваливает шаг с причиной PostgreSQL, роли backup на сервере нет`() {
        failsWith("password authentication failed for user \"ghost\"", PgAgent.configK(user = "ghost"))
    }

    @Test
    fun `Ошибка подключения или входа проваливает шаг с причиной PostgreSQL, роль без права CONNECT к базе`() {
        pg.sql("create database locked", "revoke connect on database locked from public")
        failsWith("permission denied for database \"locked\"", PgAgent.configK(database = "locked"))
    }

    @Test
    fun `Ошибка подключения или входа проваливает шаг с причиной PostgreSQL, pg_hba сервера не пускает роль`() {
        val closed = PgServer(sard, 18, alias = "db-hba").start()
        val hba = closed.query("show hba_file", "postgres")
        // Only the local connections of the container remain: nothing matches a host of the network.
        val exit = closed.exec("sh", "-c", "printf 'local all all trust\\n' > $hba")
        assertEquals(0, exit.exitCode, exit.stderr)
        closed.sql("select pg_reload_conf()")
        failsWith("no pg_hba.conf entry", PgAgent.configK(host = "db-hba"))
    }

    @Test
    fun `Ошибка подключения или входа проваливает шаг с причиной PostgreSQL, имя хоста не разрешается`() {
        failsWith("could not translate host name \"db-missing\"", PgAgent.configK(host = "db-missing"))
    }

    @Test
    fun `Ошибка подключения или входа проваливает шаг с причиной PostgreSQL, на порту сервера никто не слушает`() {
        failsWith("Connection refused", PgAgent.configK("\"port\":5999"))
    }

    @Test
    fun `Ошибка подключения или входа проваливает шаг с причиной PostgreSQL, tls_mode require а сервер без TLS`() {
        failsWith("server does not support SSL", PgAgent.configK(tlsMode = "require"))
    }

    @Test
    fun `Ошибка подключения или входа проваливает шаг с причиной PostgreSQL, сертификат сервера на другое имя`() {
        agent.container.copyFileToContainer(Transferable.of(tls.certificate), CA_FILE)
        failsWith("does not match host name", PgAgent.configK("\"tls_root_cert\":\"$CA_FILE\"", host = "db-tls", tlsMode = "verify-full"))
    }

    @Test
    fun `Ошибка подключения или входа проваливает шаг с причиной PostgreSQL, CA из tls_root_cert не тот`() {
        agent.container.copyFileToContainer(Transferable.of(tls.otherCertificate), OTHER_CA_FILE)
        failsWith("certificate verify failed", PgAgent.configK("\"tls_root_cert\":\"$OTHER_CA_FILE\"", host = "db-tls", tlsMode = "verify-full"))
    }

    @Test
    fun `Настоящий pg_dump 14 против сервера 18 проваливает шаг до дампа`() {
        val old = PgAgent(sard, 14, "old-client")
        val before = old.snapshots().size
        val started = old.backup("old-${UUID.randomUUID()}", PgAgent.configK())
        val step = old.finish(started)
        assertEquals("failed", step.status, step.message)
        val message = step.message.orEmpty()
        assertTrue("14." in message && "18." in message, "the message does not name both versions: $message")
        assertEquals(before, old.snapshots().size)
    }

    @Test
    fun `Таблица без права чтения проваливает шаг без снимка и называется`() {
        limitedDatabase()
        val before = agent.snapshots().size
        val started = agent.backup("secret-${UUID.randomUUID()}", PgAgent.configK(database = "perm", user = "limited"))
        val step = agent.finish(started)
        assertEquals("failed", step.status, step.message)
        val message = step.message.orEmpty()
        assertTrue("pg_dump" in message && "secret_t" in message, "the message does not name pg_dump and the table: $message")
        assertEquals(before, agent.snapshots().size)
        assertEquals(0, agent.locks())
        assertNull(Backups.snapshot(sard, started.stepId))
    }

    @Test
    fun `Таблица без права чтения под исключением не мешает бэкапу`() {
        limitedDatabase()
        val started = agent.backup("excluded-${UUID.randomUUID()}", PgAgent.configK("\"exclude_tables\":[\"secret_t\"]", database = "perm", user = "limited"))
        val step = agent.finish(started)
        assertEquals("succeeded", step.status, step.message)
    }

    @Test
    fun `Шаблон исключения, которому ничего не соответствует, не ошибка`() {
        val started = agent.backup("nothing-${UUID.randomUUID()}", PgAgent.configK("\"exclude_tables\":[\"nothing_*\"]"))
        assertEquals("succeeded", agent.finish(started).status)
    }

    @Test
    fun `Пустая база сохраняется и восстанавливается`() {
        pg.sql("create database empty")
        val started = agent.backup("empty-${UUID.randomUUID()}", PgAgent.configK(database = "empty"))
        val step = agent.finish(started)
        assertEquals("succeeded", step.status, step.message)
        val dump = agent.restored(checkNotNull(step.snapshotId), "/empty.dump")
        pg.put("/tmp/empty.dump", dump)
        pg.sql("create database empty_restored")
        val restore = pg.exec("pg_restore", "-U", "postgres", "-d", "empty_restored", "/tmp/empty.dump")
        assertEquals(0, restore.exitCode, restore.stderr)
    }

    @Test
    fun `Два источника на одну базу бэкапятся одновременно`() {
        val first = agent.backup("twin-a-${UUID.randomUUID()}", PgAgent.configK())
        val second = agent.backup("twin-b-${UUID.randomUUID()}", PgAgent.configK())
        val a = agent.finish(first)
        val b = agent.finish(second)
        assertEquals(listOf("succeeded", "succeeded"), listOf(a.status, b.status), "${a.message} ${b.message}")
        assertNotEquals(a.snapshotId, b.snapshotId)
    }

    @Test
    fun `Пароль не виден в списке процессов во время дампа`() {
        bigDatabase()
        val started = agent.backup("ps-${UUID.randomUUID()}", PgAgent.configK(database = "big"))
        Await.until("the phase UPLOADING of step ${started.stepId}") { agent.phase(started) == "uploading" }
        val processes = agent.processes()
        assertFalse(PG_PASSWORD in processes, "the password is in the command line of a process: $processes")
        assertTrue("--dbname=host=" in processes, "no pg_dump with a connection string in the process list: $processes")
        val step = agent.finish(started)
        assertEquals("succeeded", step.status, step.message)
        assertFalse(PG_PASSWORD in step.message.orEmpty())
        assertFalse(PG_PASSWORD in stepLog(started), "the password is in the log of the step")
        assertFalse(PG_PASSWORD in agent.container.logs, "the password is in the log of the agent")
    }

    @Test
    fun `Остановка сервера PostgreSQL посреди дампа проваливает шаг без снимка`() {
        val victim = PgServer(sard, 18, alias = "db-victim").start()
        victim.sql("create role backup login password ${'$'}${'$'}$PG_PASSWORD${'$'}${'$'}", "grant pg_read_all_data to backup", "create database big")
        victim.sql(BIG_TABLE, database = "big")
        val before = agent.snapshots().size
        val started = agent.backup("victim-${UUID.randomUUID()}", PgAgent.configK(database = "big", host = "db-victim"))
        Await.until("the phase UPLOADING of step ${started.stepId}") { agent.phase(started) == "uploading" }
        victim.container.dockerClient.killContainerCmd(victim.container.containerId).exec()
        val step = agent.finish(started)
        assertEquals("failed", step.status, step.message)
        val message = step.message.orEmpty()
        assertTrue("pg_dump" in message && "connection" in message.lowercase(), "the message does not name pg_dump and the lost connection: $message")
        assertEquals(before, agent.snapshots().size)
        assertEquals(0, agent.locks())
    }

    /** The step failed in PREPARING with [reason] from PostgreSQL, without the password and without a snapshot. */
    private fun failsWith(
        reason: String,
        config: String,
    ) {
        val before = agent.snapshots().size
        val started = agent.backup("fails-${UUID.randomUUID()}", config)
        val step = agent.finish(started)
        assertEquals("failed", step.status, step.message)
        val message = step.message.orEmpty()
        assertTrue(reason in message, "the message does not contain \"$reason\": $message")
        assertFalse(PG_PASSWORD in message, "the password is in the message")
        assertFalse(PG_PASSWORD in stepLog(started), "the password is in the log of the step")
        assertEquals("preparing", agent.phase(started))
        assertNull(step.snapshotId)
        assertEquals(before, agent.snapshots().size)
    }

    /** A database `perm` where the role `limited` reads `t1` but not `secret_t`, and the role is no `pg_read_all_data` member. */
    private fun limitedDatabase() {
        if (pg.query("select count(*) from pg_database where datname = 'perm'", "postgres") == "1") return
        pg.sql("create role limited login password ${'$'}${'$'}$PG_PASSWORD${'$'}${'$'}", "create database perm")
        pg.sql(
            "create table t1(x int)",
            "insert into t1 values (1)",
            "create table secret_t(x int)",
            "revoke all on secret_t from public",
            "grant select on t1 to limited",
            database = "perm",
        )
    }

    /** A database `big` whose dump takes longer than 10 seconds. */
    private fun bigDatabase() {
        if (pg.query("select count(*) from pg_database where datname = 'big'", "postgres") == "1") return
        pg.sql("create database big")
        pg.sql(BIG_TABLE, database = "big")
    }

    private fun stepLog(started: Backups.Started): String =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT text FROM step_logs WHERE step_id = ? ORDER BY seq").use { query ->
                query.setObject(1, started.stepId)
                query.executeQuery().use { rows -> generateSequence { if (rows.next()) rows.getString(1) else null }.joinToString("\n") }
            }
        }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private const val CA_FILE = "/etc/sard/pg-ca.pem"
        private const val OTHER_CA_FILE = "/etc/sard/pg-other-ca.pem"

        /** About 1.4 GB of random text: the dump runs for more than 10 seconds. */
        private const val BIG_TABLE = "create table big as select g, repeat(md5(g::text), 30) v from generate_series(1, 1500000) g"

        private class Tls(
            val certificate: ByteArray,
            val otherCertificate: ByteArray,
        )

        /** One TLS server for the two scenarios that need it; it is made when the first of them runs. */
        private val tls: Tls by lazy { tlsServer() }

        private lateinit var pg: PgServer
        private lateinit var agent: PgAgent

        /** A server with TLS and a certificate for another name than its own; the certificates of it and of an unrelated CA. */
        private fun tlsServer(): Tls {
            val server = PgServer(sard, 18, alias = "db-tls").start()
            val script =
                """
                set -e
                cd /var/lib/postgresql
                openssl req -new -x509 -days 2 -nodes -subj /CN=other-name -out server.crt -keyout server.key
                openssl req -new -x509 -days 2 -nodes -subj /CN=unrelated-ca -out other.crt -keyout other.key
                chown postgres:postgres server.crt server.key
                chmod 0600 server.key
                """.trimIndent()
            val made = server.exec("sh", "-c", script)
            assertEquals(0, made.exitCode, made.stderr)
            server.sql(
                "alter system set ssl_cert_file = '/var/lib/postgresql/server.crt'",
                "alter system set ssl_key_file = '/var/lib/postgresql/server.key'",
                "alter system set ssl = on",
                "select pg_reload_conf()",
            )
            val certificate = server.container.copyFileFromContainer("/var/lib/postgresql/server.crt") { it.readAllBytes() }
            val other = server.container.copyFileFromContainer("/var/lib/postgresql/other.crt") { it.readAllBytes() }
            return Tls(certificate, other)
        }

        @JvmStatic
        @BeforeAll
        fun start() {
            pg = PgServer(sard, 18).start()
            pg.seed()
            agent = PgAgent(sard, 18, "pg18")
            assertNotNull(Backups.repositoryId(sard, agent.agent.agentId, PgAgent.REPOSITORY))
        }
    }
}
