// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.stream.TestAgent
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.PluginAction
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RepositoryEntry
import dev.sard.server.registration.VerifierReport
import dev.sard.server.role.AgentRole
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.concurrent.thread
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val CYCLE = """{"cron":"30 4 * * *","timezone":"UTC"}"""

/** V6: the duration of a record, and a record whose evidence the database cleared (DB2, ADR 0069). */
@RestApiTest
class VerificationEvidenceApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val agent =
        world.agent(
            tenant,
            snapshotOf(repositories = listOf(RepositoryEntry("qa", "local", SEED_REPOSITORY_ID, ""))),
            hostname = "db1.example.com",
        )
    private val verifier =
        world.agent(
            tenant,
            snapshotOf(
                plugins = listOf(filesPlugin(actions = listOf(PluginAction.VERIFY))),
                repositories = listOf(RepositoryEntry("shared", "s3", SEED_REPOSITORY_ID, "")),
                machineId = OTHER_MACHINE_ID,
            ).copy(verifier = VerifierReport(null, null)),
            hostname = "vrf1.example.com",
            role = AgentRole.VERIFIER,
        )
    private val seed = VerificationSeed(world, tenant, admin, verifier)
    private val now = world.clock.now

    @AfterTest
    fun `drop the tenants`() {
        jdbc.update("delete from verification_schedule")
        world.close()
    }

    private fun verification(id: UUID) = world.api.get("/api/v1/verifications/$id", admin).json

    private fun utc() = world.api.send("PUT", "/api/v1/verification-schedule", admin, CYCLE)

    private fun report(
        source: UUID,
        query: String = "",
    ) = world.api.get("/api/v1/sources/$source/verification-report$query", admin)

    private fun day(text: String): Instant = Instant.parse("${text}T12:00:00Z")

    private fun onDay(
        source: UUID,
        text: String,
        stages: String = "[]",
    ) = seed.record(source, agent, VerificationSeed.Record("passed", day(text), stages = stages))

    @Test
    fun `a record carries the same duration as its line of the report, null without stage times`() {
        utc()
        val source = seed.source("db-main", agent)
        val stages =
            """[{"level":1,"name":"restic_check","state":"passed","startedAt":"2026-10-20T11:58:30Z",""" +
                """"finishedAt":"2026-10-20T12:00:00Z","exitCode":0,"output":"ok","imageDigest":""}]"""
        val timed = onDay(source, "2026-10-20", stages = stages)
        val untimed = onDay(source, "2026-10-21")

        val items = report(source, "?from=2026-10-01&to=2026-10-30").json.path("items")

        for ((id, want) in listOf(timed to "90000", untimed to null)) {
            val line = items.first { it.path("id").asString() == id.toString() }.path("durationMs")
            val shown = verification(id).path("durationMs")
            assertEquals(want, shown.takeUnless(JsonNode::isNull)?.asString(), "the record")
            assertEquals(want, line.takeUnless(JsonNode::isNull)?.asString(), "the report")
        }
    }

    @Test
    fun `a record whose evidence the database cleared is shown without stages, mismatches and duration`() {
        utc()
        val source = seed.source("db-main", agent)
        val facts = listOf(SeedFact("tables", "exact", "42"), SeedFact("database_size", "info", "900"))
        val snapshot = seed.snapshot(source, agent, fingerprint = fingerprintOf(facts))
        val earlier = now.minus(Duration.ofHours(1))
        val cleared = VerificationSeed.Record("failed", earlier, snapshot = snapshot, reason = "the copy differs")
        val record = seed.record(source, agent, cleared)

        val shown = verification(record)
        val line =
            report(source)
                .json
                .path("items")
                .first { it.path("id").asString() == record.toString() }

        assertEquals("failed", shown.path("status").asString())
        assertEquals("the copy differs", shown.path("reason").asString())
        assertEquals(emptyList(), shown.path("stages").list())
        assertEquals(emptyList(), shown.path("mismatches").list())
        assertTrue(shown.path("durationMs").isNull && line.path("durationMs").isNull)
        assertEquals(listOf("not_compared", "not_compared"), shown.pluck("fingerprint", "outcome"))
    }
}
