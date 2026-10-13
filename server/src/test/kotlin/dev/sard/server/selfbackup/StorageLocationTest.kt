// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** F4b, rule "Адрес хранилища без учётных данных…": the pure decision, whatever the scheme and its case. */
@MutFlowTest
class StorageLocationTest {
    @Test
    fun `Адрес без учётных данных и управляющих символов допустим`() {
        val addresses =
            listOf(
                "s3:acme-sard/prod",
                "s3:https://minio.example.net:9000/acme-sard/prod",
                "sftp:backup@backup.example.net:/srv/sard",
                "SFTP:backup@backup.example.net:/srv/sard",
                "sftp://backup@backup.example.net:2222//srv/sard",
                "rest:https://backup.example.net/repo",
                "/var/lib/sard-self/self",
                "x".repeat(1024),
            )
        for (address in addresses) assertTrue(MutFlow.underTest { isSafeLocation(address) }, address)
    }

    @Test
    fun `Адрес с паролем в user-password-at недопустим при любой схеме и любом регистре`() {
        val addresses =
            listOf(
                "s3:https://AKIA123:secret@minio.example.net/acme",
                "s3:HTTPS://AKIA123:secret@minio.example.net/acme",
                "S3:https://AKIA:secret@host/b",
                "rest:https://user:pass@host/repo",
                "REST:http://user:pass@host/repo",
                "sftp:backup:pw@backup.example.net:/srv/sard",
                "sftp://backup:pw@backup.example.net/srv",
                "foo://user:pw@host/x",
                "backup:pw@host:/srv",
            )
        for (address in addresses) assertFalse(MutFlow.underTest { isSafeLocation(address) }, address)
    }

    @Test
    fun `Адрес http с именем пользователя без пароля недопустим, потому что это токен`() {
        val addresses =
            listOf("https://ghp_TOKEN@host/r", "s3:https://AKIA123@minio.example.net/acme", "rest:HTTP://t@h/r")
        for (address in addresses) assertFalse(MutFlow.underTest { isSafeLocation(address) }, address)
    }

    @Test
    fun `Адрес с управляющими символами, пустой или длиннее 1024 знаков недопустим`() {
        val addresses = listOf("s3:acme-sard\nprod", "s3:acme\tsard", "s3:acme\u0000", "x".repeat(1025), "")
        for (address in addresses) assertFalse(MutFlow.underTest { isSafeLocation(address) }, address)
    }
}
