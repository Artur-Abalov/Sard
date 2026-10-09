// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import dev.sard.server.runs.SystemRole
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.ZoneId
import kotlin.test.Test
import kotlin.test.assertEquals

private val SETTINGS = SelfBackupProperties().settings("jdbc:postgresql://postgres:5432/sard", ZoneId.of("UTC"))

/** F6: the two system sources, one per role, as the server creates and keeps them. */
@MutFlowTest(includeTargets = [SelfBackupPlan::class])
class SelfBackupPlanTest {
    @Test
    fun `База - плагин postgresql ролью только на чтение, пароль по имени секрета, без глобальных объектов`() {
        val database = MutFlow.underTest { SelfBackupPlan(SETTINGS).source(SystemRole.SELF_DATABASE) }
        assertEquals("Sard: database", database.name)
        assertEquals("postgresql", database.plugin)
        assertEquals(
            mapOf(
                "host" to "postgres",
                "port" to 5432,
                "database" to "sard",
                "user" to "sard_self",
                "password_ref" to "sard-db",
                "tls_mode" to "disable",
                "pg_dump_path" to "/usr/lib/postgresql/18/bin/pg_dump",
                "include_globals" to false,
            ),
            database.config,
        )
    }

    @Test
    fun `Ключи и конфигурация - плагин files, каталог CA и каталог установки без ручных бэкапов`() {
        val keys = MutFlow.underTest { SelfBackupPlan(SETTINGS).source(SystemRole.SELF_KEYS) }
        assertEquals("Sard: keys and configuration", keys.name)
        assertEquals("files", keys.plugin)
        assertEquals(
            mapOf(
                "paths" to listOf("/var/lib/sard/pki", "/etc/sard/install"),
                "exclude" to listOf("*.dump", "*.tgz", "*.tar", "*.tar.gz", "*.zst"),
            ),
            keys.config,
        )
    }

    @Test
    fun `Источников два, по одному на роль, в порядке ролей`() {
        val roles = MutFlow.underTest { SelfBackupPlan(SETTINGS).sources().map { it.role } }
        assertEquals(listOf(SystemRole.SELF_DATABASE, SystemRole.SELF_KEYS), roles)
    }
}
