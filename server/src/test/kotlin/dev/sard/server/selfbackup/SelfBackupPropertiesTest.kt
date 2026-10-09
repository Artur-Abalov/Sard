// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.scheduler.CronSchedule
import dev.sard.server.scheduler.ScheduleDraft
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.ZoneId
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

private const val COMPOSE_URL = "jdbc:postgresql://postgres:5432/sard"
private val MOSCOW: ZoneId = ZoneId.of("Europe/Moscow")

/**
 * F6: what the two system sources are made of, from `sard.self-backup.*` and the server's own datasource. The cron
 * and zone checks are F3a's, mutated by its own tests.
 */
@MutFlowTest(excludeTargets = [CronSchedule::class, CronSchedule.Companion::class])
class SelfBackupPropertiesTest {
    @Test
    fun `По умолчанию база - та же, что у сервера, роль sard_self и секрет sard-db`() {
        val settings = MutFlow.underTest { SelfBackupProperties().settings(COMPOSE_URL, MOSCOW) }
        assertEquals(DatabaseTarget("postgres", 5432, "sard"), settings.database)
        assertEquals("sard_self", settings.databaseUser)
        assertEquals("sard-db", settings.passwordSecret)
        assertEquals("disable", settings.databaseTlsMode)
        assertEquals("/usr/lib/postgresql/18/bin/pg_dump", settings.pgDumpPath)
        assertEquals("/var/lib/sard/pki", settings.pkiDir)
        assertEquals("/etc/sard/install", settings.installDir)
    }

    @Test
    fun `Расписание по умолчанию - раз в сутки ночью в часовом поясе сервера`() {
        val settings = MutFlow.underTest { SelfBackupProperties().settings(COMPOSE_URL, MOSCOW) }
        assertEquals(ScheduleDraft("0 3 * * *", "Europe/Moscow", true), settings.schedule)
    }

    @Test
    fun `Расписание, пояс и база задаются настройками`() {
        val properties =
            SelfBackupProperties(
                cron = "30 1 * * *",
                timezone = "UTC",
                databaseHost = "db.internal",
                databasePort = 6432,
                databaseName = "sard_prod",
            )
        val settings = MutFlow.underTest { properties.settings(COMPOSE_URL, MOSCOW) }
        assertEquals(ScheduleDraft("30 1 * * *", "UTC", true), settings.schedule)
        assertEquals(DatabaseTarget("db.internal", 6432, "sard_prod"), settings.database)
    }

    @Test
    fun `Недопустимое расписание называет настройку`() {
        val cron =
            assertFailsWith<IllegalArgumentException> {
                MutFlow.underTest { SelfBackupProperties(cron = "every night").settings(COMPOSE_URL, MOSCOW) }
            }
        assertTrue("SARD_SELF_BACKUP_CRON" in cron.message.orEmpty(), cron.message)
        val zone =
            assertFailsWith<IllegalArgumentException> {
                MutFlow.underTest { SelfBackupProperties(timezone = "Mars/Olympus").settings(COMPOSE_URL, MOSCOW) }
            }
        assertTrue("SARD_SELF_BACKUP_TIMEZONE" in zone.message.orEmpty(), zone.message)
    }

    @Test
    fun `Адрес базы берётся из JDBC URL сервера`() {
        val cases =
            mapOf(
                "jdbc:postgresql://postgres:5432/sard" to DatabaseTarget("postgres", 5432, "sard"),
                "jdbc:postgresql://db.example/sard?sslmode=require" to DatabaseTarget("db.example", 5432, "sard"),
                "jdbc:postgresql://a:6432,b:6433/app" to DatabaseTarget("a", 6432, "app"),
                "jdbc:postgresql://[::1]:5433/sard" to DatabaseTarget("::1", 5433, "sard"),
                "jdbc:postgresql:sard" to DatabaseTarget("localhost", 5432, "sard"),
                "jdbc:postgresql://localhost/" to DatabaseTarget("localhost", 5432, "postgres"),
            )
        for ((url, expected) in cases) {
            assertEquals(expected, MutFlow.underTest { databaseOf(url) }, url)
        }
    }

    @Test
    fun `Непонятный JDBC URL без явной базы называет настройки`() {
        val e =
            assertFailsWith<IllegalArgumentException> {
                MutFlow.underTest { SelfBackupProperties().settings("jdbc:mysql://db/sard", MOSCOW) }
            }
        assertTrue("SARD_SELF_BACKUP_DATABASE_HOST" in e.message.orEmpty(), e.message)
    }

    @Test
    fun `Имя базы можно задать при адресе из JDBC URL`() {
        val settings = MutFlow.underTest { SelfBackupProperties(databaseName = "other").settings(COMPOSE_URL, MOSCOW) }
        assertEquals(DatabaseTarget("postgres", 5432, "other"), settings.database)
    }

    @Test
    fun `Явный адрес базы без имени - база sard`() {
        val settings = MutFlow.underTest { SelfBackupProperties(databaseHost = "db").settings(COMPOSE_URL, MOSCOW) }
        assertEquals(DatabaseTarget("db", 5432, "sard"), settings.database)
    }

    @Test
    fun `Явный адрес базы не требует понятного JDBC URL`() {
        val properties = SelfBackupProperties(databaseHost = "db", databasePort = 5432, databaseName = "sard")
        val settings = MutFlow.underTest { properties.settings("jdbc:mysql://db/sard", MOSCOW) }
        assertEquals(DatabaseTarget("db", 5432, "sard"), settings.database)
    }
}
