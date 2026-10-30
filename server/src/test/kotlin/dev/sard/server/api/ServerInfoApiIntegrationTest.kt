// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.web.server.LocalServerPort
import tools.jackson.databind.ObjectMapper
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Rule "Без расширений сервер сообщает бесплатную редакцию и пустой список" (server-core-extensions.feature). */
@RestApiTest
class ServerInfoApiIntegrationTest(
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
) {
    private val api = ApiClient(port, mapper)

    @Test
    fun `Статус ядра сообщает редакцию free`() {
        val response = api.get("/api/v1/status", null)

        assertEquals(200, response.status, response.toString())
        assertEquals("""{"id":"free","displayName":"Free"}""", response.json.path("edition").toString())
    }

    @Test
    fun `Статус не сообщает ни расширений ни лицензии`() {
        val json = api.get("/api/v1/status", null).json

        assertFalse(json.has("extensions"))
        assertFalse(json.has("licence"))
    }

    @Test
    fun `Прежние поля статуса ядра не изменились`() {
        val json = api.get("/api/v1/status", null).json

        assertTrue(json.path("version").isString)
        assertTrue(json.has("lastVerifiedRestoreAt"))
        assertTrue(json.path("lastVerifiedRestoreAt").isNull)
    }

    @Test
    fun `Сведения о сервере ядра - редакция free пустой список расширений и лицензия null`() {
        val response = api.get("/api/v1/server", api.signIn())

        assertEquals(200, response.status, response.toString())
        assertEquals("""{"id":"free","displayName":"Free"}""", response.json.path("edition").toString())
        assertEquals("[]", response.json.path("extensions").toString())
        assertTrue(response.json.has("licence"))
        assertTrue(response.json.path("licence").isNull)
    }

    @Test
    fun `Сведения о сервере без сессии отвечают 401`() {
        val response = api.get("/api/v1/server", null)

        assertEquals(401, response.status, response.toString())
        assertEquals("unauthenticated", response.code)
    }
}
