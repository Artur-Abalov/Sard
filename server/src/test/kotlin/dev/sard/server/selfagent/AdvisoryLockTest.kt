// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import com.zaxxer.hikari.HikariDataSource
import dev.sard.server.recovery.RecoveryCluster
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.postgresql.ds.PGSimpleDataSource
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Rule "Одновременный старт двух серверов ... создаёт роль один раз": the second setup waits for the first. */
@MutFlowTest
class AdvisoryLockTest {
    private fun lock() =
        AdvisoryLock(
            PGSimpleDataSource().apply {
                setURL(RecoveryCluster.container.jdbcUrl)
                user = RecoveryCluster.container.username
                password = RecoveryCluster.container.password
            },
        )

    @Test
    fun `Второй захват ждёт, пока первый не отпустит блокировку`() {
        val firstIn = CountDownLatch(1)
        val release = CountDownLatch(1)
        val secondIn = CountDownLatch(1)
        val first = Thread { lock().exclusive { firstIn.countDown().also { release.await() } } }
        val second = Thread { lock().exclusive { secondIn.countDown() } }

        MutFlow.underTest {
            first.start()
            try {
                assertTrue(firstIn.await(30, TimeUnit.SECONDS))
                second.start()
                assertFalse(secondIn.await(1, TimeUnit.SECONDS), "the second action ran while the first held the lock")
            } finally {
                release.countDown()
            }
            assertTrue(secondIn.await(30, TimeUnit.SECONDS))
            first.join()
            second.join()
        }
    }

    @Test
    fun `Блокировка отпускается и после ошибки действия`() {
        val failure = runCatching { MutFlow.underTest { lock().exclusive { error("boom") } } }.exceptionOrNull()
        var ran = 0

        MutFlow.underTest { lock().exclusive { ran++ } }

        assertEquals("boom", failure?.message)
        assertEquals(1, ran)
    }

    @Test
    fun `Блокировка снимается и у соединения, которое возвращается в пул, а не закрывается`() {
        HikariDataSource()
            .apply {
                jdbcUrl = RecoveryCluster.container.jdbcUrl
                username = RecoveryCluster.container.username
                password = RecoveryCluster.container.password
                maximumPoolSize = 1
            }.use { pool ->
                MutFlow.underTest { AdvisoryLock(pool).exclusive { } }

                val free =
                    RecoveryCluster.container.createConnection("").use { c ->
                        c.createStatement().use { s ->
                            s.executeQuery("select pg_try_advisory_lock(${0x5A4D_5E1F})").use { rs ->
                                rs.next()
                                rs.getBoolean(1)
                            }
                        }
                    }
                assertTrue(free, "the lock stayed with the pooled connection")
            }
    }
}
