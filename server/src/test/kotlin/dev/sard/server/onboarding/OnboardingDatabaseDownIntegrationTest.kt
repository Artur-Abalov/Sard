// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.api.SESSION_COOKIE
import dev.sard.server.api.list
import org.junit.jupiter.api.io.TempDir
import java.io.IOException
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.nio.file.Path
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.concurrent.thread
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

/**
 * A TCP relay between the server and its database that can be cut and restored at the same address: the database
 * is "unavailable" and then "back" without the server being told, as for a real outage.
 */
class DatabaseRelay(
    private val targetHost: String,
    private val targetPort: Int,
) : AutoCloseable {
    private val loopback = InetAddress.getLoopbackAddress()
    val port: Int = ServerSocket(0, 50, loopback).use { it.localPort }
    private val sockets = CopyOnWriteArrayList<Socket>()
    private var server: ServerSocket? = null

    fun up() {
        val listening = ServerSocket()
        listening.reuseAddress = true
        listening.bind(java.net.InetSocketAddress(loopback, port))
        server = listening
        thread(isDaemon = true) {
            while (!listening.isClosed) {
                val client = runCatching { listening.accept() }.getOrNull() ?: break
                val backend =
                    runCatching { Socket(targetHost, targetPort) }.getOrNull() ?: run {
                        client.close()
                        null
                    } ?: continue
                sockets += client
                sockets += backend
                pipe(client, backend)
                pipe(backend, client)
            }
        }
    }

    private fun pipe(
        from: Socket,
        to: Socket,
    ) = thread(isDaemon = true) {
        try {
            from.getInputStream().transferTo(to.getOutputStream())
        } catch (_: IOException) {
            // the other end went away
        } finally {
            runCatching { to.close() }
            runCatching { from.close() }
        }
    }

    fun down() {
        server?.close()
        sockets.forEach { runCatching { it.close() } }
        sockets.clear()
    }

    override fun close() = down()
}

/**
 * The scenarios of docs/specs/server/onboarding-setup.feature about a database that is not there: the answer is
 * 503 and nothing changes, and when the database is back the wizard goes on where it was.
 */
class OnboardingDatabaseDownIntegrationTest {
    @TempDir
    lateinit var tmp: Path

    private lateinit var relay: DatabaseRelay
    private lateinit var server: RunningServer

    @BeforeTest
    fun `a server whose database is behind a relay`() {
        val real = TestPostgres.newDatabase()
        val target = java.net.URI(real.url.removePrefix("jdbc:"))
        relay = DatabaseRelay(target.host, target.port).also { it.up() }
        val behind = Database("jdbc:postgresql://localhost:${relay.port}${target.path}", real.user, real.password)
        server =
            Installation(tmp, behind).start(
                StartOptions(
                    properties =
                        mapOf(
                            "spring.datasource.hikari.connection-timeout" to 1000,
                            "spring.datasource.hikari.validation-timeout" to 500,
                        ),
                ),
            )
    }

    @AfterTest
    fun `stop the server and the relay`() {
        runCatching { server.close() }
        relay.close()
    }

    private val client get() = server.client

    private fun <T> databaseDown(block: () -> T): T {
        relay.down()
        try {
            return block()
        } finally {
            relay.up()
        }
    }

    private fun state() =
        client
            .state()
            .json
            .path("steps")
            .list()
            .map { it.path("state").asString() }

    @Test
    fun `Состояние онбординга при недоступной базе отвечает 503`() {
        val reply = databaseDown { client.state() }

        assertEquals(503, reply.status)
        assertEquals("unavailable", reply.code)
        assertEquals("application/problem+json", reply.contentType)
    }

    @Test
    fun `Подтверждение CA при недоступной базе отвечает 503 и сохраняет сессию настройки`() {
        val session = client.setupSession()

        val reply = databaseDown { client.confirmCa(session) }

        assertEquals(503, reply.status)
        assertEquals("unavailable", reply.code)
        assertTrue(reply.setCookies().isEmpty())
        assertEquals("pending", state().first())
        assertEquals(
            "setup",
            client
                .state(session)
                .json
                .path("access")
                .asString(),
        )
    }

    @Test
    fun `Шаг admin при недоступной базе отвечает 503 и ничего не меняет`() {
        val session = client.setupSession()
        client.confirmCa(session)

        val reply = databaseDown { client.admin(session, OWNER_PASSWORD) }

        assertEquals(503, reply.status)
        assertEquals("unavailable", reply.code)
        assertTrue(reply.setCookies().isEmpty())
        assertEquals("pending", state()[1])
        assertEquals(
            "setup",
            client
                .state(session)
                .json
                .path("access")
                .asString(),
        )
        assertEquals(204, client.enterCode(CODE).status)
    }

    @Test
    fun `Смена пароля при недоступной базе отвечает 503 и ничего не меняет`() {
        val a = client.completeWizard()
        val b = checkNotNull(client.login(OWNER_PASSWORD).cookie(SESSION_COOKIE))

        val reply = databaseDown { client.changePassword(a, OWNER_PASSWORD, "new-password-2026") }

        assertEquals(503, reply.status)
        assertEquals("unavailable", reply.code)
        assertNull(reply.setCookie(SESSION_COOKIE))
        assertTrue(client.alive(b))
        assertEquals(204, client.login(OWNER_PASSWORD).status)
    }

    @Test
    fun `Вход при недоступной базе отвечает 503 и не засчитывается в перебор`() {
        client.completeWizard()
        repeat(4) { assertEquals(401, client.login("wrong-password-123").status) }

        repeat(3) {
            val reply = databaseDown { client.login("wrong-password-123") }
            assertEquals(503, reply.status)
            assertEquals("unavailable", reply.code)
        }

        assertEquals(401, client.login("wrong-password-123").status)
    }
}
