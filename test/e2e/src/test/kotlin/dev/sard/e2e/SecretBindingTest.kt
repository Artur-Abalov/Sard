// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.BeforeAll
import org.junit.jupiter.api.extension.RegisterExtension
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * S11, docs/specs/agent/secret-binding.feature: the secret `pg-app` goes only where the operator of the host bound
 * it, only over SCRAM, and to a server on the network only over TLS that checks the certificate unless the binding
 * allows less. A real server, the real agent with its psql and pg_dump 18, steps started through the REST API.
 */
class SecretBindingTest {
    @Test
    fun `Цель вне привязки — шаг падает до подключения, сервер не получает ни одного подключения`() {
        val unbound = PgServer(sard, 18, alias = "db-unbound").start()
        unbound.sql("alter system set log_connections = on", "select pg_reload_conf()")
        val step = failed(bound, PgAgent.configK(host = "db-unbound"))
        val message = step.message.orEmpty()
        assertTrue(
            "secret \"pg-app\" is not allowed for postgresql at db-unbound:5432/app" in message &&
                "sudo sard-agent secret set pg-app --plugin postgresql --target db-unbound:5432/app" in message,
            message,
        )
        val logs = unbound.container.logs
        assertFalse(Regex("connection received: host=(?!\\[local])").containsMatchIn(logs), "the server saw a connection:\n$logs")
    }

    @Test
    fun `Консоль предупреждает об источнике, который привязка секрета не разрешает`() {
        val started = bound.backup("warned-${UUID.randomUUID()}", PgAgent.configK(host = "db-elsewhere"))
        val source = SardApi(sard).get("/api/v1/sources/${started.sourceId}")
        assertEquals(200, source.status)
        assertTrue("is not allowed for postgresql at db-elsewhere:5432/app" in source.body, source.body)
        bound.finish(started)
    }

    @Test
    fun `Сервер просит md5 — агент не отдаёт пароль и говорит, что делать`() {
        val md5 = PgServer(sard, 18, alias = "db-md5").start()
        md5.seed()
        val hba = md5.query("show hba_file", "postgres")
        val exit = md5.exec("sh", "-c", "printf 'local all all trust\\nhost all all all md5\\n' > $hba")
        assertEquals(0, exit.exitCode, exit.stderr)
        md5.sql("alter system set password_encryption = 'md5'", "select pg_reload_conf()")
        md5.sql("alter role backup password ${'$'}${'$'}$PG_PASSWORD${'$'}${'$'}")
        val message = failed(bound, PgAgent.configK(host = "db-md5")).message.orEmpty()
        assertTrue("server requested a hashed password" in message && "the password was not sent" in message, message)
    }

    @Test
    fun `tls_mode disable к серверу в сети без разрешения в привязке — отказ до подключения`() {
        val message = failed(strict, PgAgent.configK()).message.orEmpty()
        assertTrue("tls_mode disable would send the password of secret \"pg-app\" to db:5432 in clear text" in message, message)
        assertTrue("--allow-tls disable" in message, message)
    }

    @Test
    fun `Секрет без привязки не отдаётся ни одному плагину`() {
        val message = failed(unboundAgent, PgAgent.configK()).message.orEmpty()
        assertTrue("secret \"pg-app\" has no binding on this host and is not given to any plugin" in message, message)
    }

    @Test
    fun `Привязанный секрет к своей цели — бэкап проходит`() {
        val started = bound.backup("bound-${UUID.randomUUID()}", PgAgent.configK())
        val step = bound.finish(started)
        assertEquals("succeeded", step.status, step.message)
    }

    /** The step of a backup of [config] by [agent] fails in PREPARING, the password nowhere in its message. */
    private fun failed(
        agent: PgAgent,
        config: String,
    ): Backups.Step {
        val started = agent.backup("refused-${UUID.randomUUID()}", config)
        val step = agent.finish(started)
        assertEquals("failed", step.status, step.message)
        assertFalse(PG_PASSWORD in step.message.orEmpty(), "the password is in the message")
        assertEquals("preparing", agent.phase(started))
        return step
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private lateinit var bound: PgAgent
        private lateinit var strict: PgAgent
        private lateinit var unboundAgent: PgAgent

        @JvmStatic
        @BeforeAll
        fun start() {
            PgServer(sard, 18).start().seed()
            bound = PgAgent(sard, 18, "bound")
            strict = PgAgent(sard, 18, "strict", bindings = mapOf("pg-app" to AgentHost.binding("postgresql", listOf("db"), allowTls = null)))
            unboundAgent = PgAgent(sard, 18, "unbound", bindings = emptyMap())
        }
    }
}
