// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.database.MigrationSandbox
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals

private const val BEFORE_F4B = "202610121200"

// F4b itself: later migrations (V1 makes agents.role NOT NULL) are not what this class checks.
private const val F4B = "202610121300"
private const val SCHEMA = "sard"
private val TENANT = UUID.fromString("00000000-0000-0000-0000-000000000001")

/**
 * Migration V202610121300 over what a version before F4b wrote (docs/specs/server/onboarding-self-backup.feature,
 * Р21, rule "Обновлённая установка блокирует только регистрацию новых агентов"): the first start with F4b decides
 * once whether the installation is an upgraded one. Works in a schema of its own.
 */
class OnboardingMarksMigrationTest {
    private val sandbox = MigrationSandbox()
    private val connection = sandbox.connection
    private val single = sandbox.dataSource
    private val jdbc = JdbcTemplate(single)

    @AfterTest
    fun `close the connection`() {
        connection.close()
    }

    private fun flyway(target: String? = null) = sandbox.flyway(target)

    private fun agent(
        builtin: Boolean,
        revoked: Boolean,
    ): UUID {
        val id = UUID.randomUUID()
        val revokedAt = Timestamp.from(Instant.parse("2026-10-11T12:00:00Z")).takeIf { revoked }
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, registered_at, builtin, revoked_at) " +
                "values (?, ?, ?, now(), ?, ?)",
            id,
            TENANT,
            "host",
            builtin,
            revokedAt,
        )
        return id
    }

    private fun source(
        agent: UUID,
        deleted: Boolean = false,
        systemRole: String? = null,
    ) {
        jdbc.update(
            """
            insert into sources (id, tenant_id, agent_id, name, plugin, config, repository_name, created_at, updated_at,
                deleted_at, system_role)
            values (?, ?, ?, ?, 'files', '{}'::jsonb, 'qa', now(), now(), ?, ?)
            """.trimIndent(),
            UUID.randomUUID(),
            TENANT,
            agent,
            "source-${UUID.randomUUID()}",
            Timestamp.from(Instant.now()).takeIf { deleted },
            systemRole,
        )
    }

    /** The database of a version before F4b holding what [inserting] writes, started with F4b. */
    private fun upgradeWith(inserting: () -> Unit): Boolean {
        flyway(BEFORE_F4B).migrate()
        jdbc.execute("set search_path to $SCHEMA")
        jdbc.execute("set session_replication_role = replica")
        inserting()
        flyway().migrate()
        jdbc.execute("set search_path to $SCHEMA")
        return JdbcOnboardingSteps(jdbc).upgraded()
    }

    @Test
    fun `Первый старт версии с F4b - живой обычный агент делает установку обновлённой`() {
        assertEquals(true, upgradeWith { agent(builtin = false, revoked = false) })
    }

    @Test
    fun `Первый старт версии с F4b - неудалённый источник администратора делает установку обновлённой`() {
        assertEquals(true, upgradeWith { source(agent(builtin = true, revoked = false)) })
    }

    @Test
    fun `Первый старт версии с F4b - только встроенный агент оставляет установку чистой`() {
        assertEquals(false, upgradeWith { agent(builtin = true, revoked = false) })
    }

    @Test
    fun `Первый старт версии с F4b - отозванный агент и удалённый источник оставляют установку чистой`() {
        val clean =
            upgradeWith {
                val revoked = agent(builtin = false, revoked = true)
                source(revoked, deleted = true)
            }

        assertEquals(false, clean)
    }

    @Test
    fun `Системные источники самобэкапа не делают установку обновлённой`() {
        val clean = upgradeWith { source(agent(builtin = true, revoked = false), systemRole = "self_database") }

        assertEquals(false, clean)
    }

    @Test
    fun `Отметка остаётся после удаления агентов и источников и не ставится позже`() {
        assertEquals(true, upgradeWith { agent(builtin = false, revoked = false) })
        jdbc.update("delete from agents")
        flyway().migrate()
        jdbc.execute("set search_path to $SCHEMA")

        assertEquals(true, JdbcOnboardingSteps(jdbc).upgraded())
    }

    @Test
    fun `Агент, появившийся после первого старта, отметку не ставит`() {
        flyway(F4B).migrate()
        jdbc.execute("set search_path to $SCHEMA")
        jdbc.execute("set session_replication_role = replica")
        agent(builtin = false, revoked = false)
        flyway().migrate()
        jdbc.execute("set search_path to $SCHEMA")

        assertEquals(false, JdbcOnboardingSteps(jdbc).upgraded())
    }
}
