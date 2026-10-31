// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.sample

import dev.sard.server.SardServerApplication
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.ContextConfiguration
import tools.jackson.databind.ObjectMapper
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val RUN_DEFAULTS = """{"backupTimeoutMinutes":481,"restoreTimeoutMinutes":601}"""

/** The subject source of the sample (server-core-extensions.feature, Р26, @sample @qa). */
@SpringBootTest(
    classes = [SardServerApplication::class],
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.sample.listener=record",
        "sard.sample.subject=sso-user:alice",
        "sard.session.sweep-interval=1h",
    ],
)
@Import(SampleInstallation::class)
@ContextConfiguration(initializers = [FreshPkiDirectory::class])
class SampleSubjectIntegrationTest(
    @Autowired mapper: ObjectMapper,
    @Autowired private val jdbc: JdbcTemplate,
    @LocalServerPort port: Int,
) {
    private val client = SampleClient(port, mapper)

    private fun details(type: String) =
        jdbc.queryForList("select detail from sample_events where type = ? order by id", String::class.java, type).map { it.orEmpty() }

    @BeforeTest
    fun `an empty log of events`() {
        jdbc.update("delete from sample_events")
    }

    @Test
    fun `Субъект источника образца становится actor события настройки изменены`() {
        val session = checkNotNull(client.signIn())
        jdbc.update("delete from sample_events")

        assertEquals(200, client.send("PUT", "/api/v1/run-defaults", session, RUN_DEFAULTS).status)

        val detail = details("settings.changed").single()
        assertTrue("setting=run_defaults" in detail && "actor=sso-user:alice" in detail, detail)
    }

    @Test
    fun `Субъект источника образца становится actor события выход`() {
        val session = checkNotNull(client.signIn())
        jdbc.update("delete from sample_events")

        assertEquals(204, client.send("DELETE", "/api/v1/session", session).status)

        assertTrue("actor=sso-user:alice" in details("signout").single())
    }

    @Test
    fun `Успешный вход не спрашивает источник субъекта образца`() {
        checkNotNull(client.signIn())

        assertTrue("actor=administrator:admin" in details("signin.succeeded").single())
    }

    @Test
    fun `Источник субъекта образца не открывает API без сессии ядра`() {
        assertEquals(401, client.send("GET", "/api/v1/sources").status)
    }
}
