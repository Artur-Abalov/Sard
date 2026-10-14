// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.recovery

import ch.qos.logback.classic.Level
import dev.sard.server.onboarding.Database
import dev.sard.server.onboarding.Installation
import dev.sard.server.onboarding.RunningServer
import dev.sard.server.onboarding.StartOptions
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Files
import java.nio.file.Path
import java.sql.DriverManager
import java.sql.SQLException
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * Rule "Роль дампа sard_self возвращается при старте на восстановленной базе" of
 * docs/specs/server/server-recovery.feature (Р33, OQ-214): real servers over a database without the role.
 */
class SelfRoleRestoredDatabaseIntegrationTest {
    @TempDir
    lateinit var tmp: Path

    private val running = mutableListOf<RunningServer>()

    @AfterTest
    fun `stop every server`() {
        running.forEach { runCatching { it.close() } }
    }

    private fun start(
        installation: Installation,
        channel: Boolean = true,
    ): RunningServer {
        val properties = if (channel) mapOf("sard.self-agent.dir" to installation.selfDir.toString()) else emptyMap()
        return installation.start(StartOptions(properties = properties)).also { running += it }
    }

    private fun installation(database: Database = RecoveryCluster.restoredDatabase()) = Installation(tmp, database)

    private fun loginAs(
        password: String,
        database: Database,
    ) = DriverManager.getConnection(database.url, "sard_self", password).use { }

    @Test
    fun `На восстановленной базе без роли sard_self сервер создаёт роль, и она входит паролем из канала`() {
        val installation = installation()

        start(installation)

        loginAs(Files.readString(installation.selfDir.resolve("db-password")), installation.database)
    }

    private fun query(
        database: Database,
        sql: String,
    ): List<String> =
        DriverManager.getConnection(database.url, database.user, database.password).use { c ->
            c.createStatement().use { s ->
                s.executeQuery(sql).use { rs -> generateSequence { if (rs.next()) rs.getString(1) else null }.toList() }
            }
        }

    private fun roleShape(database: Database) =
        query(
            database,
            "select concat_ws(' ', rolcanlogin, rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls, " +
                "rolconnlimit, rolconfig) from pg_roles where rolname = 'sard_self'",
        ) +
            query(
                database,
                "select r.rolname from pg_auth_members m join pg_roles r on r.oid = m.roleid " +
                    "join pg_roles u on u.oid = m.member where u.rolname = 'sard_self'",
            )

    @Test
    fun `Созданная при старте роль sard_self повторяет атрибуты миграции`() {
        val installation = installation()

        start(installation)

        assertEquals(
            listOf("t f f f f f 4 {default_transaction_read_only=on}", "pg_read_all_data"),
            roleShape(installation.database),
        )
    }

    @Test
    fun `Созданная при старте роль sard_self ничего не пишет даже без режима только чтения`() {
        val installation = installation()
        start(installation)
        val password = Files.readString(installation.selfDir.resolve("db-password"))

        for (sql in listOf(
            "update tenants set name = name",
            "create table x (i int)",
            "create temp table x (i int)",
            "select lo_create(0)",
        )) {
            val error =
                assertFailsWith<SQLException>(sql) {
                    DriverManager.getConnection(installation.database.url, "sard_self", password).use { c ->
                        c.createStatement().use {
                            it.execute("set default_transaction_read_only = off")
                            it.execute(sql)
                        }
                    }
                }
            assertEquals("42501", error.sqlState, "$sql: ${error.message}")
        }
    }

    @Test
    fun `Созданная при старте роль sard_self читает каждую таблицу базы`() {
        val installation = installation()
        start(installation)
        val tables = query(installation.database, "select table_name from information_schema.tables where table_schema = 'public'")
        assertTrue(tables.isNotEmpty())

        DriverManager
            .getConnection(installation.database.url, "sard_self", Files.readString(installation.selfDir.resolve("db-password")))
            .use { c -> c.createStatement().use { s -> tables.forEach { s.executeQuery("select count(*) from \"$it\"").close() } } }
    }

    @Test
    fun `Повторный старт с готовой ролью не меняет её и не пишет предупреждений`() {
        val installation = installation()
        val first = start(installation)
        val shape = roleShape(installation.database)
        first.close()
        running -= first

        val second = start(installation)

        assertEquals(shape, roleShape(installation.database))
        val warnings = second.startLog.filter { it.level == Level.WARN && "docs/operations/self-agent.md" in it.text }
        assertEquals(emptyList(), warnings)
    }

    @Test
    fun `Без канала сервер роль sard_self не создаёт`() {
        val installation = installation()

        start(installation, channel = false)

        assertEquals(emptyList(), query(installation.database, "select rolname from pg_roles where rolname = 'sard_self'"))
    }

    @Test
    fun `Пользователь сервера без прав суперпользователя получает одно предупреждение, сервер стартует`() {
        val installation = installation(RecoveryCluster.restoredDatabase(superuser = false))

        val server = start(installation)

        val warnings = server.startLog.filter { it.level == Level.WARN && "docs/operations/self-agent.md" in it.text }
        assertEquals(1, warnings.size, warnings.toString())
        val roles =
            RecoveryCluster.container.createConnection("").use { c ->
                c.createStatement().use {
                    it.executeQuery("select count(*) from pg_roles where rolname = 'sard_self'").use { rs ->
                        rs.next()
                        rs.getInt(1)
                    }
                }
            }
        assertEquals(0, roles)
    }

    @Test
    fun `Одновременный старт двух серверов на восстановленной базе создаёт роль один раз`() {
        val installation = installation()
        start(installation, channel = false).also {
            it.close()
            running -= it
        }
        val pool = Executors.newFixedThreadPool(2)
        try {
            val started = (1..2).map { pool.submit<RunningServer> { start(installation) } }.map { it.get(2, TimeUnit.MINUTES) }
            assertEquals(2, started.size)
        } finally {
            pool.shutdownNow()
        }

        assertEquals(1, query(installation.database, "select count(*) from pg_roles where rolname = 'sard_self'").single().toInt())
        loginAs(Files.readString(installation.selfDir.resolve("db-password")), installation.database)
    }

    @Test
    fun `Пароль созданной роли не попадает в лог сервера и журнал PostgreSQL`() {
        val installation = installation()

        val server = start(installation)

        val password = Files.readString(installation.selfDir.resolve("db-password"))
        assertFalse(password in server.logText)
        assertFalse(password in RecoveryCluster.container.logs)
    }
}
