// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import dev.sard.server.EmbeddedPostgres
import org.testcontainers.containers.Container.ExecResult
import org.testcontainers.utility.MountableFile
import java.io.File
import java.sql.DriverManager
import java.sql.SQLException
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

private val HEX = Regex("[0-9a-f]{64}")
private val CONSUMERS = mapOf("sard_migrator" to "migrate", "sard_app" to "app", "sard_backup" to "backup")

/**
 * The script of the db-init service (deploy/db/db-init.sh), run as in the compose file: by the superuser over the local
 * socket of a PostgreSQL container of the pinned image. Rules "Роли и пароли встроенной установки создаёт разовый шаг
 * db-init" of docs/specs/server/database-roles.feature; the stack itself is checked by scripts/test-self-agent.sh.
 */
class DbInitScriptTest {
    private lateinit var cluster: Cluster

    private val container get() = cluster.container

    @BeforeTest
    fun `a cluster that logs every statement`() {
        cluster =
            Cluster(
                EmbeddedPostgres
                    .container()
                    .withCommand(
                        "postgres",
                        "-c",
                        "log_statement=all",
                    ).also { it.start() },
            )
        container.copyFileToContainer(
            MountableFile.forHostPath(File("../deploy/db/db-init.sh").absolutePath),
            "/tmp/db-init.sh",
        )
        container.copyFileToContainer(
            MountableFile.forHostPath(File("../deploy/db/bootstrap.sql").absolutePath),
            "/tmp/bootstrap.sql",
        )
        sh("mkdir -p " + CONSUMERS.values.joinToString(" ") { "/tmp/pw/$it" } + " /tmp/pw/channel")
    }

    @AfterTest
    fun `stop the cluster`() = cluster.close()

    private fun sh(command: String): ExecResult = container.execInContainer("sh", "-c", command)

    /** The script as the compose file runs it, with the superuser of this container. */
    private fun initialise(user: String = container.username): ExecResult =
        sh(
            "PGHOST=/var/run/postgresql PGUSER=$user PGDATABASE=${container.databaseName} SARD_DB_PASSWORDS=/tmp/pw " +
                "SARD_DB_BOOTSTRAP=/tmp/bootstrap.sql sh /tmp/db-init.sh",
        )

    private fun password(role: String): String = sh("cat /tmp/pw/${CONSUMERS.getValue(role)}/password").stdout

    private fun logsIn(
        role: String,
        password: String,
    ): Boolean =
        try {
            DriverManager.getConnection(cluster.url(), role, password).use { it.createStatement().execute("select 1") }
            true
        } catch (_: SQLException) {
            false
        }

    @Test
    fun `Новая установка получает пароли ролей без участия администратора`() {
        val result = initialise()

        assertEquals(0, result.exitCode, result.stderr)
        for (role in CONSUMERS.keys) {
            assertTrue(HEX.matches(password(role)), "$role: '${password(role)}'")
            assertTrue(logsIn(role, password(role)), role)
        }
        assertEquals(
            3,
            CONSUMERS.keys
                .map(::password)
                .toSet()
                .size,
            "a password for each role",
        )
    }

    @Test
    fun `Файл пароля 64 hex-символа, владелец 10001, права 0400`() {
        initialise()

        for (dir in CONSUMERS.values) {
            assertEquals("400 10001 10001 64", sh("stat -c '%a %u %g %s' /tmp/pw/$dir/password").stdout.trim(), dir)
        }
    }

    @Test
    fun `Пароль роли хранится в PostgreSQL как SCRAM`() {
        initialise()

        val verifiers =
            cluster.query(
                "select string_agg(left(rolpassword, 13), ',' order by rolname) from pg_authid where rolname in " +
                    "('sard_app', 'sard_backup', 'sard_migrator')",
            ) {
                it.getString(1)
            }
        assertEquals("SCRAM-SHA-256,SCRAM-SHA-256,SCRAM-SHA-256", verifiers)
    }

    @Test
    fun `Пароль не попадает ни в вывод скрипта, ни в журнал PostgreSQL`() {
        val result = initialise()

        for (role in CONSUMERS.keys) {
            assertFalse(password(role) in result.stdout + result.stderr, role)
            assertFalse(password(role) in container.logs, role)
        }
    }

    @Test
    fun `Повторный запуск не меняет пароли и каталог базы и молчит о предупреждениях`() {
        initialise()
        val passwords = CONSUMERS.keys.associateWith(::password)
        val verifiers =
            cluster.query(
                "select string_agg(rolpassword, ',' order by rolname) from pg_authid where rolname like 'sard\\_%'",
            ) {
                it.getString(1)
            }
        val catalog = cluster.catalog()

        val again = initialise()

        assertEquals(0, again.exitCode, again.stderr)
        assertEquals(passwords, CONSUMERS.keys.associateWith(::password))
        assertEquals(
            verifiers,
            cluster.query(
                "select string_agg(rolpassword, ',' order by rolname) from pg_authid where rolname like 'sard\\_%'",
            ) {
                it.getString(1)
            },
        )
        assertEquals(catalog, cluster.catalog())
        assertFalse(
            Regex("WARN|ERROR", RegexOption.IGNORE_CASE).containsMatchIn(again.stdout + again.stderr),
            again.stdout + again.stderr,
        )
    }

    @Test
    fun `Удалённый файл пароля роли даёт роли новый пароль`() {
        initialise()
        val old = password("sard_app")

        sh("rm /tmp/pw/app/password")
        val result = initialise()

        assertEquals(0, result.exitCode, result.stderr)
        val fresh = password("sard_app")
        assertTrue(HEX.matches(fresh))
        assertNotEquals(old, fresh)
        assertTrue(logsIn("sard_app", fresh))
        assertFalse(logsIn("sard_app", old))
    }

    @Test
    fun `Файл пароля есть, а у роли пароля нет, и роль получает пароль из файла`() {
        cluster.bootstrap()
        for (dir in CONSUMERS.values) sh("printf '%s' ${"ab".repeat(32)} > /tmp/pw/$dir/password")
        cluster.admin().use { c ->
            for (role in CONSUMERS.keys) {
                c.createStatement().use {
                    it.execute("alter role $role password null")
                }
            }
        }

        val result = initialise()

        assertEquals(0, result.exitCode, result.stderr)
        assertTrue(CONSUMERS.keys.all { logsIn(it, "ab".repeat(32)) })
    }

    @Test
    fun `В канале остаётся пустая заглушка db-password для пароля соседа`() {
        initialise()

        assertEquals("0", sh("wc -c < /tmp/pw/channel/db-password").stdout.trim())
    }

    @Test
    fun `Неудачный bootstrap останавливает db-init, лог называет причину и раздел 16`() {
        cluster.admin().use { it.createStatement().execute("create role weak login createdb password 'weak'") }

        val result = initialise("weak")

        assertNotEquals(0, result.exitCode)
        assertTrue("docs/operator/16-postgresql.md" in result.stderr, result.stderr)
        assertEquals("", sh("ls /tmp/pw/app").stdout.trim(), "no password was made")
    }

    @Test
    fun `Каталог внешнего кластера совпадает с каталогом встроенной установки`() {
        initialise()
        cluster.admin().use { c ->
            c.createStatement().use {
                it.execute("alter role sard_migrator password '${Preparation.password("sard_migrator")}'")
            }
        }
        Preparation.migrate(cluster.url())
        Cluster().use { external ->
            external.bootstrap()
            Preparation.migrate(external.url())

            assertEquals(cluster.catalog(), external.catalog())
        }
    }
}
