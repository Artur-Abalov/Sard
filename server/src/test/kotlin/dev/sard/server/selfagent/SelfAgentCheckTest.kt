// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import ch.qos.logback.classic.Level
import dev.sard.server.enrollment.EnrollmentSecret
import dev.sard.server.enrollment.EnrollmentToken
import dev.sard.server.pki.CaFingerprint
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.util.concurrent.CyclicBarrier
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private val FINGERPRINT = CaFingerprint("a".repeat(64))

private fun tokenOf(n: Int) = EnrollmentToken(EnrollmentSecret(ByteArray(32) { n.toByte() }), FINGERPRINT).encode()

private fun hashOf(token: String) = EnrollmentToken.parse(token).secret.hash()

/** The store of built-in tokens as the check sees it: at most one usable, the last one issued. */
private class FakeTokens : BuiltinTokens {
    val issued = AtomicInteger()
    var failWith: RuntimeException? = null
    var delayMillis = 0L

    @Volatile
    private var current: ByteArray? = null

    override fun usable(secretHash: ByteArray): Boolean {
        failWith?.let { throw it }
        return current?.contentEquals(secretHash) == true
    }

    override fun replace(): String {
        failWith?.let { throw it }
        Thread.sleep(delayMillis)
        val token = tokenOf(issued.incrementAndGet())
        current = hashOf(token)
        return token
    }

    /** A token that is usable and was issued earlier. */
    fun preIssued(): String = replace()
}

private class FakeAgents : BuiltinAgents {
    var live = false
    var failWith: RuntimeException? = null

    override fun live(): Boolean {
        failWith?.let { throw it }
        return live
    }
}

/** Rule "Проверка выпускает встроенный токен, только пока нет живого встроенного агента": one check. */
class SelfAgentCheckTest {
    @TempDir
    lateinit var dir: Path

    private val tokens = FakeTokens()
    private val agents = FakeAgents()

    private val channel by lazy { SelfChannel(dir) }
    private val check by lazy { SelfAgentCheck(channel, tokens, agents) }

    private val file get() = dir.resolve("enroll-token")

    @Test
    fun `Проверка без встроенного агента и без файла выпускает токен и пишет его без перевода строки`() {
        check.run()

        assertEquals(1, tokens.issued.get())
        assertEquals(tokenOf(1), Files.readString(file))
    }

    @Test
    fun `Пригодный токен в файле не заменяется`() {
        val token = tokens.preIssued()
        channel.writeToken(token)

        check.run()

        assertEquals(1, tokens.issued.get())
        assertEquals(token, Files.readString(file))
    }

    @Test
    fun `Токен базы без файла заменяется новым`() {
        tokens.preIssued()

        check.run()

        assertEquals(2, tokens.issued.get())
        assertEquals(tokenOf(2), Files.readString(file))
    }

    @Test
    fun `Файл не от активного встроенного токена заменяется новым`() {
        val contents: List<(String) -> String> =
            listOf(
                { _ -> tokenOf(99) },
                { active -> active.take(47) + (if (active[47] == 'A') 'Q' else 'A') + active.drop(48) },
                { _ -> "это не токен" },
                { _ -> "" },
                { active -> active + "\n" },
            )
        for (content in contents) {
            val active = tokens.preIssued()
            channel.writeToken(content(active))
            val before = tokens.issued.get()

            check.run()

            assertEquals(before + 1, tokens.issued.get(), content(active))
            assertEquals(tokenOf(before + 1), Files.readString(file))
        }
    }

    @Test
    fun `Десять проверок подряд выпускают один токен`() {
        repeat(10) { check.run() }

        assertEquals(1, tokens.issued.get())
        assertEquals(tokenOf(1), Files.readString(file))
    }

    @Test
    fun `Одновременные проверки выпускают один токен`() {
        tokens.delayMillis = 200
        val barrier = CyclicBarrier(2)
        val pool = Executors.newFixedThreadPool(2)
        try {
            val runs =
                List(2) {
                    pool.submit {
                        barrier.await()
                        check.run()
                    }
                }
            runs.forEach { it.get() }
        } finally {
            pool.shutdownNow()
        }

        assertEquals(1, tokens.issued.get())
        assertEquals(tokenOf(1), Files.readString(file))
    }

    @Test
    fun `Живой встроенный агент удаляет файл токена и ничего не выпускает`() {
        channel.writeToken(tokenOf(7))
        agents.live = true

        check.run()

        assertFalse(Files.exists(file))
        assertEquals(0, tokens.issued.get())
    }

    @Test
    fun `Живой встроенный агент без файла токена ничего не меняет`() {
        agents.live = true

        check.run()

        assertFalse(Files.exists(file))
        assertEquals(0, tokens.issued.get())
    }

    @Test
    fun `Отозванный встроенный агент не мешает выпуску`() {
        agents.live = true
        check.run()
        agents.live = false

        check.run()

        assertEquals(tokenOf(1), Files.readString(file))
    }

    @Test
    fun `Запись файла токена отмечается в логе без строки токена`() {
        val lines = captureEvents { check.run() }

        val written = lines.filter { "built-in agent enrollment token written to " in it.text }
        assertEquals(1, written.size)
        assertTrue("$file" in written.single().text)
        val secret = tokenOf(1).substringAfter("sard_").substringBefore('.')
        assertTrue(lines.none { tokenOf(1) in it.text || secret in it.text })
    }

    @Test
    fun `Отказ базы не меняет канал, даёт одну строку WARN и не бросает`() {
        val token = tokens.preIssued()
        channel.writeToken(token)
        agents.failWith = IllegalStateException("database down: $token")

        val lines = captureEvents { check.run() }

        assertEquals(token, Files.readString(file))
        val warnings = lines.filter { it.level == Level.WARN }
        assertEquals(1, warnings.size)
        assertTrue("built-in agent check" in warnings.single().text, warnings.single().text)
        assertTrue(lines.none { token in it.text })
    }

    @Test
    fun `Отказ базы при выпуске не оставляет файла, а после восстановления проверка выпускает токен`() {
        tokens.failWith = IllegalStateException("database down")
        check.run()
        assertFalse(Files.exists(file))

        tokens.failWith = null
        check.run()

        assertEquals(tokenOf(1), Files.readString(file))
    }
}
