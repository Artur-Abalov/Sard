// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import dev.sard.server.onboarding.Database
import dev.sard.server.onboarding.Installation
import dev.sard.server.onboarding.RunningServer
import dev.sard.server.onboarding.StartOptions
import dev.sard.server.onboarding.TestPostgres
import dev.sard.server.pki.MovableClock
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.time.Instant
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val JUNE_15 = Instant.parse("2027-06-15T12:00:00Z")

/** Rule "Секции логов шагов ведёт служебная функция" of docs/specs/server/database-roles.feature: real servers. */
class StepLogPartitionsStartIntegrationTest {
    @TempDir
    lateinit var tmp: Path

    private val running = java.util.Collections.synchronizedList(mutableListOf<RunningServer>())

    @AfterTest
    fun `stop every server`() {
        running.forEach { runCatching { it.close() } }
    }

    private fun start(
        database: Database,
        at: Instant = JUNE_15,
    ): RunningServer =
        Installation(tmp, database)
            .start(StartOptions(clock = MovableClock(at), properties = mapOf("sard.scheduler.interval" to "1h")))
            .also { running += it }

    private fun partitions(database: Database): List<String> =
        TestPostgres.databases().let { cluster ->
            val name = database.url.substringAfterLast('/')
            cluster.admin(name).use { c ->
                c.createStatement().use { s ->
                    s
                        .executeQuery(
                            "select c.relname from pg_inherits i join pg_class c on c.oid = i.inhrelid where " +
                                "i.inhparent = 'sard.step_logs'::regclass order by 1",
                        ).use { rs ->
                            generateSequence { if (rs.next()) rs.getString(1) else null }.toList()
                        }
                }
            }
        }

    @Test
    fun `При старте есть секции текущего и двух следующих месяцев`() {
        val database = TestPostgres.newDatabase()

        start(database)

        val names = partitions(database)
        assertTrue(
            listOf("step_logs_2027_06", "step_logs_2027_07", "step_logs_2027_08").all { it in names },
            names.toString(),
        )
    }

    @Test
    fun `Секция, созданная сервером, принадлежит sard_owner, а DEFAULT-секции нет`() {
        val database = TestPostgres.newDatabase()
        start(database)
        val name = database.url.substringAfterLast('/')

        val owner =
            TestPostgres.databases().query(
                "select relowner::regrole::text from pg_class where relname = 'step_logs_2027_08'",
                name,
            ) {
                it.getString(1)
            }
        val defaults =
            TestPostgres.databases().query(
                "select count(*) from pg_inherits i join pg_class c on c.oid = i.inhrelid where i.inhparent = " +
                    "'sard.step_logs'::regclass and pg_get_expr(c.relpartbound, c.oid) = 'DEFAULT'",
                name,
            ) {
                it.getInt(1)
            }

        assertEquals("sard_owner", owner)
        assertEquals(0, defaults)
    }

    @Test
    fun `Два сервера одновременно создают одну секцию без ошибки`() {
        val database = TestPostgres.newDatabase()
        // The installation exists already (its CA is made) and has no partition of the months of the clock below.
        start(database, Instant.parse("2027-01-10T12:00:00Z")).close()
        val pool = Executors.newFixedThreadPool(2)
        val servers = (1..2).map { pool.submit<RunningServer> { start(database) } }

        val started = servers.map { it.get(180, TimeUnit.SECONDS) }
        pool.shutdown()

        assertEquals(2, started.size)
        assertEquals(1, partitions(database).count { it == "step_logs_2027_08" })
    }
}
