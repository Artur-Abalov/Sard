// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val DOC = "docs/operator/06-data-and-backup.md#восстановление-из-самобэкапа-f7"
private const val REPO = "https://github.com/Artur-Abalov/Sard/blob"

/** F4b, rules "Карточка восстановления…" and "Адрес хранилища без учётных данных…": the pure decisions. */
@MutFlowTest
class RecoveryCardsTest {
    @Test
    fun `Карточка версии релиза ссылается на тег этой версии`() {
        for (version in listOf("0.1.0", "0.1.0-beta.1", "1.20.3-rc.12")) {
            assertEquals("$REPO/v$version/$DOC", MutFlow.underTest { recoveryProcedureUrl(version) }, version)
        }
    }

    @Test
    fun `Карточка версии без тега ссылается на ветку main`() {
        for (version in listOf("0.0.0-dev", "dev", "unknown", "0.1.0-rc", "0.1.0-beta.0", "01.2.3", "")) {
            assertEquals("$REPO/main/$DOC", MutFlow.underTest { recoveryProcedureUrl(version) }, version)
        }
    }

    @Test
    fun `Адрес без учётных данных и управляющих символов допустим`() {
        val addresses =
            listOf(
                "s3:acme-sard/prod",
                "s3:https://minio.example.net:9000/acme-sard/prod",
                "sftp:backup@backup.example.net:/srv/sard",
                "sftp://backup@backup.example.net:2222//srv/sard",
                "/var/lib/sard-self/self",
                "x".repeat(1024),
            )
        for (address in addresses) assertTrue(MutFlow.underTest { isSafeLocation(address) }, address)
    }

    @Test
    fun `Адрес с учётными данными, управляющими символами или длиннее 1024 знаков недопустим`() {
        val addresses =
            listOf(
                "s3:https://AKIA123:secret@minio.example.net/acme",
                "sftp:backup:pw@backup.example.net:/srv/sard",
                "sftp://backup:pw@backup.example.net/srv",
                "s3:acme-sard\nprod",
                "s3:acme\tsard",
                "s3:acme\u0000",
                "x".repeat(1025),
                "",
            )
        for (address in addresses) assertFalse(MutFlow.underTest { isSafeLocation(address) }, address)
    }
}
