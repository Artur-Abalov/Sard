// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.extension.FixtureEdition
import dev.sard.server.extension.FixtureEditionConfiguration
import dev.sard.server.extension.LicenceState
import dev.sard.server.extension.LicenceSummary
import dev.sard.server.extension.PILOT_VALID
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** Rule "Лицензия никогда не блокирует бэкап, восстановление и проверку": a licence of any state, a run starts. */
@RestApiTest
@Import(FixtureEditionConfiguration::class)
class LicenceNeverBlocksApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @Autowired private val edition: FixtureEdition,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent = world.agent(tenant)

    @AfterTest
    fun `the licence is valid again`() {
        edition.licence = { PILOT_VALID }
        world.close()
    }

    @Test
    fun `Ручной бэкап запускается при любой лицензии`() {
        val states =
            listOf<() -> LicenceSummary?>(
                { LicenceSummary(LicenceState.EXPIRED) },
                { LicenceSummary(LicenceState.ABSENT) },
                { LicenceSummary(LicenceState.INVALID) },
                { error("licence store unreachable") },
            )
        for (state in states) {
            edition.licence = state
            val source = world.api.post("/api/v1/sources", admin, sourceJson("s-${UUID.randomUUID()}", agent.agentId))
            val id = source.json.path("id").asString()

            val run = world.api.post("/api/v1/sources/$id/runs", admin, null)

            assertEquals(201, run.status, run.toString())
        }
    }
}
