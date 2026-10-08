// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.downloads.AgentOffer
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.install.release
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.context.annotation.Primary
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals

// Rule "Агент старше раздаваемой версии помечен как устаревший", scenarios [Р2] @http @grpc: pre-release
// versions through REST and a real Register over gRPC; one server version per class.

@TestConfiguration(proxyBeanMethods = false)
class BetaTwoRelease {
    @Bean
    @Primary
    fun betaTwo(): AgentOffer = release("v0.1.0-beta.2")
}

@TestConfiguration(proxyBeanMethods = false)
class ReleaseCandidateOne {
    @Bean
    @Primary
    fun candidate(): AgentOffer = release("v0.1.0-rc.1")
}

@TestConfiguration(proxyBeanMethods = false)
class TagOutsideTheRule {
    @Bean
    @Primary
    fun outside(): AgentOffer = release("v0.0.1-rc1")
}

@TestConfiguration(proxyBeanMethods = false)
class WithheldCandidate {
    @Bean
    @Primary
    fun withheld(): AgentOffer = AgentOffer.Withheld("v0.1.0-rc.1")
}

/** The tenant, an admin and the "outdated" of the agent that registered with [version]. */
private class OutdatedWorld(
    private val world: RestWorld,
) {
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)

    fun outdated(version: String): Boolean {
        val agent = world.agent(tenant, snapshotOf(version = version))
        return world.api
            .get("/api/v1/agents?limit=200", admin)
            .json
            .path("items")
            .list()
            .first { it.path("id").asString() == agent.agentId.toString() }
            .path("outdated")
            .asBoolean()
    }
}

/** Server v0.1.0-beta.2 serving v0.1.0-beta.2. */
@RestApiTest
@Import(BetaTwoRelease::class)
class PreReleaseBetaApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val outdated = OutdatedWorld(world)

    @AfterTest
    fun `drop the tenants`() = world.close()

    @Test
    fun `Агент на бете при раздаче следующей беты устарел`() {
        assertEquals(true, outdated.outdated("v0.1.0-beta.1"))
    }
}

/** Server v0.1.0-rc.1 serving v0.1.0-rc.1. */
@RestApiTest
@Import(ReleaseCandidateOne::class)
class PreReleaseSameApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val outdated = OutdatedWorld(world)

    @AfterTest
    fun `drop the tenants`() = world.close()

    @Test
    fun `Сервер на предрелизе не помечает агента той же версии`() {
        assertEquals(false, outdated.outdated("v0.1.0-rc.1"))
    }
}

/** Server built as v0.0.1-rc1, a tag outside the rule. */
@RestApiTest
@Import(TagOutsideTheRule::class)
class PreReleaseOutsideRuleApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val outdated = OutdatedWorld(world)

    @AfterTest
    fun `drop the tenants`() = world.close()

    @Test
    fun `Сервер с выпущенным тегом вне правила не помечает агента на бете`() {
        assertEquals(false, outdated.outdated("v0.0.1-beta.1"))
    }
}

/** SARD_AGENT_DOWNLOADS=false, server v0.1.0-rc.1. */
@RestApiTest
@Import(WithheldCandidate::class)
class PreReleaseDownloadsOffApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val outdated = OutdatedWorld(world)

    @AfterTest
    fun `drop the tenants`() = world.close()

    @Test
    fun `При выключенной раздаче предрелизный сервер сравнивает агента со своей версией`() {
        assertEquals(true, outdated.outdated("v0.1.0-beta.3"))
    }
}
