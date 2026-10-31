// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.FixtureEdition
import dev.sard.server.extension.FixtureEditionConfiguration
import dev.sard.server.extension.LicenceItem
import dev.sard.server.extension.LicenceState
import dev.sard.server.extension.LicenceSummary
import dev.sard.server.extension.PILOT_VALID
import dev.sard.server.extension.SecondExtensionConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import tools.jackson.databind.ObjectMapper
import java.time.Instant
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** Rule "Редакцию, расширения и лицензию сервер сообщает так, как их дало расширение" (spec). */
@RestApiTest
@Import(FixtureEditionConfiguration::class, SecondExtensionConfiguration::class)
class ServerInfoSampleApiIntegrationTest(
    @Autowired mapper: ObjectMapper,
    @Autowired private val edition: FixtureEdition,
    @LocalServerPort port: Int,
) {
    private val api = ApiClient(port, mapper)
    private val admin by lazy { api.signIn() }

    @AfterTest
    fun `the licence of the fixture is valid again`() {
        edition.licence = { PILOT_VALID }
    }

    private fun server() = api.get("/api/v1/server", admin)

    @Test
    fun `Статус сервера с образцом сообщает его редакцию`() {
        val json = api.get("/api/v1/status", null).json

        assertEquals("""{"id":"sample","displayName":"Sample"}""", json.path("edition").toString())
        assertFalse(json.has("extensions"))
        assertFalse(json.has("licence"))
    }

    @Test
    fun `Сведения о сервере с образцом сообщают его редакцию и его самого`() {
        val json = server().json

        assertEquals("""{"id":"sample","displayName":"Sample"}""", json.path("edition").toString())
        val sample = """{"id":"sample","displayName":"Sard Sample Extension"}"""
        assertTrue(sample in json.path("extensions").toString())
    }

    @Test
    fun `Сведения о сервере с образцом без сессии отвечают 401`() {
        assertEquals(401, api.get("/api/v1/server", null).status)
    }

    @Test
    fun `Действующая лицензия передаётся в сведения о сервере как есть`() {
        val licence = server().json.path("licence")

        assertEquals("valid", licence.path("state").asString())
        assertEquals("Pilot LLC", licence.path("holder").asString())
        assertEquals("2026-12-31T00:00:00Z", licence.path("expiresAt").asString())
        assertEquals("""[{"label":"Agents","value":"1"}]""", licence.path("items").toString())
    }

    @Test
    fun `Состояние лицензии от расширения отражается в сведениях о сервере`() {
        for (state in listOf(LicenceState.EXPIRED, LicenceState.ABSENT, LicenceState.INVALID)) {
            edition.licence = { LicenceSummary(state) }

            val response = server()

            assertEquals(200, response.status)
            assertEquals(
                state.name.lowercase(),
                response.json
                    .path("licence")
                    .path("state")
                    .asString(),
            )
        }
    }

    @Test
    fun `Лицензия без срока и владельца отдаёт их как null`() {
        edition.licence = { LicenceSummary(LicenceState.ABSENT) }

        val licence = server().json.path("licence")

        assertTrue(licence.path("holder").isNull)
        assertTrue(licence.path("expiresAt").isNull)
        assertEquals("[]", licence.path("items").toString())
    }

    @Test
    fun `Сбой шва лицензии отдаёт состояние unavailable`() {
        edition.licence = { error("licence store unreachable") }
        var response: ApiResponse? = null

        val logs = captureLogs { response = server() }

        assertEquals(200, response!!.status)
        assertEquals(
            "unavailable",
            response.json
                .path("licence")
                .path("state")
                .asString(),
        )
        assertTrue(
            response.json
                .path("extensions")
                .toString()
                .contains("sample"),
        )
        assertEquals(1, logs.count { "licence is unavailable" in it })
    }

    @Test
    fun `Сбой шва лицензии не ломает статус`() {
        edition.licence = { error("licence store unreachable") }

        val response = api.get("/api/v1/status", null)

        assertEquals(200, response.status)
        assertEquals(
            "sample",
            response.json
                .path("edition")
                .path("id")
                .asString(),
        )
        assertTrue(response.json.path("version").isString)
        assertTrue(response.json.has("lastVerifiedRestoreAt"))
    }

    @Test
    fun `Ядро не сверяет срок лицензии со своими часами`() {
        edition.licence = {
            val past = Instant.parse("2026-01-01T00:00:00Z")
            LicenceSummary(LicenceState.VALID, "Pilot LLC", past, listOf(LicenceItem("Agents", "1")))
        }

        assertEquals(
            "valid",
            server()
                .json
                .path("licence")
                .path("state")
                .asString(),
        )
    }

    @Test
    fun `Расширения в сведениях о сервере упорядочены по id`() {
        val ids = server().json.pluck("extensions", "id")

        assertEquals(listOf("aaa-test", "sample"), ids)
    }
}
