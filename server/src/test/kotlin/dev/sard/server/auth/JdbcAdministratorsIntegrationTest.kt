// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.TestcontainersConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Instant
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val NOW: Instant = Instant.parse("2026-10-09T12:00:00Z")

/** The administrator of the default tenant in the database (migration V202610091200). */
@SpringBootTest(properties = ["spring.grpc.server.port=0", "server.port=0"])
@Import(TestcontainersConfiguration::class)
class JdbcAdministratorsIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val administrators = JdbcAdministrators(jdbc)

    @AfterTest
    fun `forget the state`() {
        jdbc.update("delete from administrators")
    }

    @Test
    fun `Администратора нет, пока шаг admin не выполнен`() {
        assertNull(administrators.hash())
    }

    @Test
    fun `Администратор создаётся один раз`() {
        assertTrue(administrators.create("hash-1", NOW))
        assertFalse(administrators.create("hash-2", NOW))

        assertEquals("hash-1", administrators.hash())
    }

    @Test
    fun `Хэш заменяется, только если он всё ещё прежний`() {
        administrators.create("hash-1", NOW)

        assertFalse(administrators.replaceHash("stale", "hash-2", NOW))
        assertTrue(administrators.replaceHash("hash-1", "hash-2", NOW))

        assertEquals("hash-2", administrators.hash())
        val sql = "select password_changed_at from administrators"
        assertEquals(NOW, jdbc.queryForObject(sql, java.sql.Timestamp::class.java)?.toInstant())
    }

    @Test
    fun `Сброс удаляет хэш и сообщает, был ли он`() {
        administrators.create("hash-1", NOW)

        assertTrue(administrators.remove())
        assertFalse(administrators.remove())
        assertNull(administrators.hash())
    }
}
