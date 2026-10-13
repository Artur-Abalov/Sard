// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertIs
import kotlin.test.assertTrue

private const val FINGERPRINT = "SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU"
private const val KEY_ID_LINE = "read -r -p \"S3 access key ID: \" SARD_S3_KEY_ID"
private const val RESTART = "docker compose restart self-agent"
private const val RUN_PREFIX =
    "docker compose run --rm --no-deps -u 0 --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER " +
        "--cap-add SETUID --cap-add SETGID self-agent repo add"
private const val RUN_SUFFIX = "--no-restart --config /etc/sard/self/agent.yaml"

private fun params(text: String): Map<String, String> =
    text
        .split(' ')
        .filter { it.isNotEmpty() }
        .mapNotNull { pair -> pair.split('=', limit = 2).takeIf { it.size == 2 } }
        .associate { (name, value) -> name to value }

/** F4b, rule "Команду добавления репозитория собирает сервер, без секретов": the text of the commands. */
@MutFlowTest
class RepositoryCommandsTest {
    private fun build(
        text: String,
        name: String = "self",
    ): CommandBuild = MutFlow.underTest { RepositoryCommands.build(name, params(text)) }

    private fun built(
        text: String,
        name: String = "self",
    ): CommandBuild.Built = assertIs<CommandBuild.Built>(build(text, name), text)

    private fun runLine(built: CommandBuild.Built) = built.commands.single { it.startsWith("docker compose run") }

    @Test
    fun `Команда для хранилища содержит адрес и флаги этого хранилища`() {
        val cases =
            listOf(
                Triple(
                    "storage=s3 provider=aws region=eu-central-1 bucket=acme-sard path=prod",
                    "s3:acme-sard/prod",
                    "--provider aws --region eu-central-1 --access-key-id \"\$SARD_S3_KEY_ID\"",
                ),
                Triple(
                    "storage=s3 provider=b2 region=eu-central-003 bucket=acme-sard",
                    "s3:acme-sard",
                    "--provider b2 --region eu-central-003 --access-key-id \"\$SARD_S3_KEY_ID\"",
                ),
                Triple(
                    "storage=s3 provider=custom endpoint=https://minio.example.net:9000 bucket=acme-sard path=prod",
                    "s3:https://minio.example.net:9000/acme-sard/prod",
                    "--access-key-id \"\$SARD_S3_KEY_ID\"",
                ),
                Triple(
                    "storage=sftp user=backup host=backup.example.net path=/srv/sard",
                    "sftp:backup@backup.example.net:/srv/sard",
                    RUN_SUFFIX,
                ),
                Triple(
                    "storage=sftp user=backup host=backup.example.net port=2222 path=/srv/sard " +
                        "hostKeyFingerprint=$FINGERPRINT",
                    "sftp://backup@backup.example.net:2222//srv/sard",
                    "--host-key-fingerprint $FINGERPRINT",
                ),
                Triple("storage=local", "/var/lib/sard-self/self", RUN_SUFFIX),
            )
        for ((query, address, flags) in cases) {
            val built = built(query)

            assertEquals(address, built.address, query)
            val line = runLine(built)
            assertTrue(flags in line, "$flags in $line")
            assertTrue("self" in line.split(' '), line)
            assertTrue(address in line, "$address in $line")
            assertEquals(RESTART, built.commands.last(), query)
        }
    }

    @Test
    fun `Каждый адрес, который собирает сервер, допустим для проверки адреса хранилища`() {
        val queries =
            listOf(
                "storage=s3 provider=aws region=eu-central-1 bucket=acme-sard path=prod",
                "storage=s3 provider=aws region=eu-central-1 bucket=acme-sard",
                "storage=s3 provider=b2 region=eu-central-003 bucket=acme-sard path=a/b",
                "storage=s3 provider=b2 region=eu-central-003 bucket=acme-sard",
                "storage=s3 provider=custom endpoint=https://minio.example.net:9000 bucket=acme-sard path=prod",
                "storage=s3 provider=custom endpoint=https://minio.example.net:9000 bucket=acme-sard",
                "storage=s3 provider=custom endpoint=http://minio.example.net bucket=acme-sard path=prod",
                "storage=s3 provider=custom endpoint=https://minio.example.net bucket=acme-sard",
                "storage=sftp user=backup host=backup.example.net path=/srv/sard",
                "storage=sftp user=backup host=backup.example.net port=2222 path=/srv/sard",
                "storage=sftp user=backup host=backup.example.net path=/srv/sard hostKeyFingerprint=$FINGERPRINT",
                "storage=local",
            )
        for (query in queries) {
            val address = built(query).address

            assertTrue(isSafeLocation(address), "$address from $query")
        }
    }

    @Test
    fun `Строка docker compose run запускает разовый контейнер соседа с пятью capabilities`() {
        val built = built("storage=local")

        val line = runLine(built)
        assertTrue(line.startsWith(RUN_PREFIX), line)
        assertTrue(line.endsWith(RUN_SUFFIX), line)
        assertTrue(built.commands.none { "docker compose exec" in it }, built.commands.toString())
    }

    @Test
    fun `Адрес в строке команды стоит в одинарных кавычках, имя и простые значения - без`() {
        val line = runLine(built("storage=s3 provider=aws region=eu-central-1 bucket=acme-sard path=prod"))

        assertTrue("'s3:acme-sard/prod'" in line, line)
        assertTrue("--region eu-central-1 " in line, line)
        assertTrue(" self '" in line, line)
    }

    @Test
    fun `Команда для S3 сначала спрашивает идентификатор ключа в терминале`() {
        val built = built("storage=s3 provider=aws region=eu-central-1 bucket=acme-sard")

        assertEquals(KEY_ID_LINE, built.commands.first())
        assertEquals(3, built.commands.size)
    }

    @Test
    fun `Для SFTP и локального хранилища команд две`() {
        assertEquals(2, built("storage=local").commands.size)
        assertEquals(2, built("storage=sftp user=backup host=h.example.net path=/srv/sard").commands.size)
    }

    @Test
    fun `Команда не содержит способов передать секрет аргументом`() {
        val queries =
            listOf(
                "storage=s3 provider=aws region=eu-central-1 bucket=acme-sard",
                "storage=s3 provider=custom endpoint=http://minio.example.net bucket=acme-sard",
                "storage=sftp user=backup host=backup.example.net path=/srv/sard",
                "storage=local",
            )
        val forbidden = listOf("--secret-key", "--password", "AWS_SECRET_ACCESS_KEY", "RESTIC_PASSWORD")
        for (query in queries) {
            val text = built(query).commands.joinToString("\n")
            for (word in forbidden) assertTrue(word !in text, "$word in $text")
        }
    }

    @Test
    fun `Имя репозитория попадает в команду и в локальный адрес`() {
        val built = built("storage=local", name = "self-3")

        assertEquals("/var/lib/sard-self/self-3", built.address)
        assertTrue(" self-3 " in runLine(built), runLine(built))
    }

    @Test
    fun `Имя репозитория - первое свободное из self, self-2, self-3`() {
        val cases =
            listOf(
                emptyList<String>() to "self",
                listOf("self") to "self-2",
                listOf("self", "self-2") to "self-3",
                listOf("self-2") to "self",
                listOf("main") to "self",
                listOf("self", "self-3") to "self-2",
            )
        for ((taken, name) in cases) {
            assertEquals(name, MutFlow.underTest { RepositoryCommands.freeName(taken) }, "$taken")
        }
    }

    @Test
    fun `Недопустимый параметр команды отклоняется с именем поля`() {
        val cases =
            listOf(
                "storage=gcs" to "storage",
                "" to "storage",
                "storage=s3 provider=aws region=eu-central-1" to "bucket",
                "storage=s3 provider=aws region=eu-central-1 bucket=Acme_Sard" to "bucket",
                "storage=s3 provider=aws region=eu-central-1 bucket=ab" to "bucket",
                "storage=s3 provider=aws region=eu-central-1 bucket=acme;reboot" to "bucket",
                "storage=s3 provider=aws region=eu-central-1 bucket=-acme" to "bucket",
                "storage=s3 provider=aws region=eu-central-1 bucket=acme-" to "bucket",
                "storage=s3 provider=aws bucket=acme-sard" to "region",
                "storage=s3 provider=aws region=EU_Central bucket=acme-sard" to "region",
                "storage=s3 provider=wasabi region=eu-central-1 bucket=acme-sard" to "provider",
                "storage=s3 region=eu-central-1 bucket=acme-sard" to "provider",
                "storage=s3 provider=custom bucket=acme-sard" to "endpoint",
                "storage=s3 provider=custom endpoint=ftp://minio.example.net bucket=acme-sard" to "endpoint",
                "storage=s3 provider=custom endpoint=https://AKIA:secret@minio.example.net bucket=acme-sard" to
                    "endpoint",
                "storage=s3 provider=custom endpoint=https://minio.example.net/x?y=1 bucket=acme-sard" to "endpoint",
                "storage=s3 provider=custom endpoint=https://minio.example.net/ bucket=acme-sard" to "endpoint",
                "storage=s3 provider=custom endpoint=https://minio.example.net:99999 bucket=acme-sard" to "endpoint",
                "storage=s3 provider=aws region=eu-central-1 bucket=acme-sard path=a/../b" to "path",
                "storage=s3 provider=aws region=eu-central-1 bucket=acme-sard path=a//b" to "path",
                "storage=s3 provider=aws region=eu-central-1 bucket=acme-sard path=a/./b" to "path",
                "storage=s3 provider=aws region=eu-central-1 bucket=acme-sard path=prod/" to "path",
                "storage=s3 provider=aws region=eu-central-1 bucket=acme-sard path=it's" to "path",
                "storage=s3 provider=aws region=eu-central-1 bucket=acme-sard path=\$(id)" to "path",
                "storage=sftp host=backup.example.net path=/srv/sard" to "user",
                "storage=sftp user=backup path=/srv/sard" to "host",
                "storage=sftp user=backup host=backup.example.net" to "path",
                "storage=sftp user=backup host=backup.example.net port=70000 path=/srv/sard" to "port",
                "storage=sftp user=backup host=backup.example.net port=0 path=/srv/sard" to "port",
                "storage=sftp user=backup host=backup.example.net port=65536 path=/srv/sard" to "port",
                "storage=sftp user=backup host=backup.example.net port=22x path=/srv/sard" to "port",
                "storage=sftp user=backup host=backup.example.net path=/srv/sard hostKeyFingerprint=MD5:aa" to
                    "hostKeyFingerprint",
                "storage=sftp user=Back;up host=backup.example.net path=/srv/sard" to "user",
                "storage=sftp user=backup host=backup.example.net path=srv/sard" to "path",
                "storage=sftp user=backup host=backup.example.net path=/ " to "path",
                "storage=sftp user=backup host=back;up path=/srv/sard" to "host",
                "storage=sftp user=backup host=-bad.example.net path=/srv/sard" to "host",
            )
        for ((query, field) in cases) {
            val rejected = assertIs<CommandBuild.Rejected>(build(query), query)

            assertTrue(field in rejected.fields, "$query -> ${rejected.fields}")
        }
    }

    @Test
    fun `Отклонённый S3 называет все недопустимые поля и не строит команд`() {
        val aws = "storage=s3 provider=aws"
        val custom = "storage=s3 provider=custom"
        val cases =
            listOf(
                "$aws bucket=acme-sard" to listOf("region"),
                "$aws region=eu-central-1" to listOf("bucket"),
                "$aws region=eu-central-1 bucket=acme-sard path=a//b" to listOf("path"),
                "$aws" to listOf("region", "bucket"),
                "$aws region=EU bucket=ab path=../x" to listOf("region", "bucket", "path"),
                "storage=s3 provider=b2 bucket=acme-sard" to listOf("region"),
                "$custom bucket=acme-sard" to listOf("endpoint"),
                "$custom endpoint=https://minio.example.net" to listOf("bucket"),
                "$custom endpoint=https://minio.example.net bucket=acme-sard path=a//b" to listOf("path"),
                "$custom endpoint=ftp://x bucket=ab path=../x" to listOf("endpoint", "bucket", "path"),
                "$custom region=eu-central-1 bucket=acme-sard" to listOf("endpoint"),
                "storage=s3 region=eu-central-1 bucket=acme-sard" to listOf("provider"),
                "storage=s3 provider=wasabi" to listOf("provider", "region", "bucket"),
            )
        for ((query, fields) in cases) {
            val rejected = assertIs<CommandBuild.Rejected>(build(query), query)

            assertEquals(fields, rejected.fields, query)
        }
    }

    @Test
    fun `Параметры, которых нет у выбранного S3, не мешают`() {
        val aws = built("storage=s3 provider=aws region=eu-central-1 bucket=acme-sard endpoint=ftp://x")
        assertEquals("s3:acme-sard", aws.address)
        val custom = built("storage=s3 provider=custom endpoint=https://m.example.net bucket=acme-sard region=EU_x")
        assertEquals("s3:https://m.example.net/acme-sard", custom.address)
        assertTrue("--provider" !in runLine(custom) && "--region" !in runLine(custom), runLine(custom))
    }

    @Test
    fun `Путь из 512 знаков принимается, из 513 отклоняется`() {
        val base = "storage=s3 provider=aws region=eu-central-1 bucket=acme-sard path="

        assertIs<CommandBuild.Built>(build(base + "a".repeat(512)))
        val rejected = assertIs<CommandBuild.Rejected>(build(base + "a".repeat(513)))
        assertEquals(listOf("path"), rejected.fields)
    }

    @Test
    fun `Допустимые крайние значения принимаются`() {
        val accepted =
            listOf(
                "storage=s3 provider=aws region=a bucket=a.b",
                "storage=s3 provider=aws region=${"a".repeat(64)} bucket=${"a".repeat(63)}",
                "storage=s3 provider=custom endpoint=http://127.0.0.1:1 bucket=abc",
                "storage=s3 provider=custom endpoint=https://minio.example.net:65535 bucket=abc",
                "storage=sftp user=_a-b host=10.0.0.1 port=65535 path=/a.b_c-d/e",
                "storage=sftp user=${"a".repeat(32)} host=[::1] port=1 path=/srv",
            )
        for (query in accepted) assertIs<CommandBuild.Built>(build(query), query)
    }

    @Test
    fun `Слово командной строки - без кавычек, если в нём только буквы, цифры, точка, дефис и подчёркивание`() {
        assertEquals("self-2.a_b", shellWord("self-2.a_b"))
        assertEquals("'a b'", shellWord("a b"))
        assertEquals("'it'\\''s'", shellWord("it's"))
        assertEquals("'s3:x/y'", shellWord("s3:x/y"))
    }

    @Test
    fun `Команда показа пароля`() {
        assertEquals(
            "docker compose exec self-agent sard-agent repo password self --reveal --config /etc/sard/self/agent.yaml",
            MutFlow.underTest { passwordRevealCommand("self") },
        )
        assertEquals(
            "docker compose exec self-agent sard-agent repo password 'a b' --reveal --config /etc/sard/self/agent.yaml",
            passwordRevealCommand("a b"),
        )
    }
}
