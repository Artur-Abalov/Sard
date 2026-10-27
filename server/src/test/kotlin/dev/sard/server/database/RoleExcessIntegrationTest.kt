// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import ch.qos.logback.classic.Level
import dev.sard.server.onboarding.Database
import dev.sard.server.onboarding.Installation
import dev.sard.server.onboarding.RunningServer
import dev.sard.server.onboarding.TestPostgres
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val WARNING = "has more rights than the server needs"

/** Rule "При старте сервер предупреждает о лишних правах своей роли" of docs/specs/server/database-roles.feature. */
class RoleExcessIntegrationTest {
    @TempDir
    lateinit var tmp: Path

    private val running = mutableListOf<RunningServer>()

    @AfterTest
    fun `stop every server`() {
        running.forEach { runCatching { it.close() } }
    }

    private fun start(database: Database) = Installation(tmp, database).start().also { running += it }

    private fun RunningServer.excessSeries() =
        bean(PrometheusMeterRegistry::class.java)
            .scrape()
            .lines()
            .filter { it.startsWith("sard_database_role_excess{") }
            .associate { it.substringAfter("reason=\"").substringBefore("\"") to it.substringAfterLast(" ").toDouble() }

    private fun RunningServer.warnings() = startLog.filter { it.level == Level.WARN && WARNING in it.text }

    /** The database of an installation, and the cluster's admin connection to it for setting a role up. */
    private fun asRole(
        database: Database,
        role: String,
        password: String,
        setup: List<String>,
    ): Database {
        val name = database.url.substringAfterLast('/')
        TestPostgres.databases().admin(name).use { c ->
            c.createStatement().use { s ->
                s.execute("create role $role login password '$password'")
                setup.forEach(s::execute)
            }
        }
        return Database(database.url, role, password, database.admin)
    }

    private fun assertOneWarning(
        server: RunningServer,
        reason: String,
    ) {
        val warnings = server.warnings()
        assertEquals(1, warnings.size, server.logText)
        assertTrue(reason in warnings.single().text, warnings.single().text)
        assertTrue("docs/operator/16-postgresql.md" in warnings.single().text, warnings.single().text)
        val expected = mapOf("superuser" to 0.0, "schema_owner" to 0.0) + (reason to 1.0)
        assertEquals(expected, server.excessSeries())
    }

    @Test
    fun `Лишнее право роли сервера - суперпользователь - даёт предупреждение и метрику`() {
        val server = start(TestPostgres.newDatabase().asSuperuser())

        assertOneWarning(server, "superuser")
    }

    @Test
    fun `Лишнее право роли сервера - владелец схемы sard - даёт предупреждение и метрику`() {
        val database = TestPostgres.newDatabase()
        // The schema is the DBA's: sard_owner, whose functions work for the server, keeps the use of it.
        val setup =
            listOf(
                "alter schema sard owner to schema_boss",
                "grant all on schema sard to sard_owner",
                "grant sard_app to schema_boss",
            )
        val owner = asRole(database, "schema_boss", "boss", setup)

        assertOneWarning(start(owner), "schema_owner")
    }

    @Test
    fun `Лишнее право роли сервера - член sard_owner - даёт предупреждение и метрику`() {
        val database = TestPostgres.newDatabase()
        val member = asRole(database, "owner_member", "member", listOf("grant sard_owner to owner_member"))

        assertOneWarning(start(member), "schema_owner")
    }

    @Test
    fun `Под sard_app предупреждения нет и метрика равна нулю`() {
        val server = start(TestPostgres.newDatabase())

        assertEquals(emptyList(), server.warnings().map { it.text })
        assertFalse(server.logText.contains(WARNING))
        assertEquals(mapOf("superuser" to 0.0, "schema_owner" to 0.0), server.excessSeries())
    }
}
