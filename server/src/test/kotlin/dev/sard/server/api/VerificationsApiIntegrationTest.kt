// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

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
import tools.jackson.databind.ObjectMapper
import java.sql.Timestamp
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val REPOSITORY_ID = "e".repeat(64)

/** V4 REST: a source's verification, «verify now», the history and a record, the cycle, the verifiers. */
@RestApiTest
class VerificationsApiIntegrationTest(
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
        world.agent(tenant, snapshotOf(repositories = listOf(RepositoryEntry("qa", "local", REPOSITORY_ID, ""))))
    private val verifier =
        world.agent(
            tenant,
            snapshotOf(
                plugins = listOf(filesPlugin(actions = listOf(PluginAction.VERIFY))),
                repositories = listOf(RepositoryEntry("shared", "s3", REPOSITORY_ID, "")),
                machineId = OTHER_MACHINE_ID,
            ).copy(verifier = VerifierReport(null, null)),
            hostname = "verifier1",
            role = AgentRole.VERIFIER,
        )
    private val source = source()

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun source(): String =
        world.api
            .post("/api/v1/sources", admin, sourceJson("etc", agent.agentId))
            .json
            .path("id")
            .asString()

    /** A whole snapshot of the source, as a succeeded backup leaves it; returns snapshots.id. */
    private fun snapshot(): UUID {
        val run =
            UUID.fromString(
                world.api
                    .post("/api/v1/sources/$source/runs", admin, "")
                    .json
                    .path("id")
                    .asString(),
            )
        world.forceRun(run, "succeeded")
        val step = jdbc.queryForObject("select id from run_steps where run_id = ?", UUID::class.java, run)
        val id = UUID.randomUUID()
        jdbc.update(
            "insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id, " +
                "snapshot_id, total_bytes, added_bytes, created_at, partial) " +
                "values (?, ?, ?, ?, ?, 'qa', ?, ?, 10, 1, ?, false)",
            id,
            tenant,
            UUID.fromString(source),
            step,
            agent.agentId,
            REPOSITORY_ID,
            "snap-$id",
            Timestamp.from(world.clock.now),
        )
        return id
    }

    private fun verification() = world.api.get("/api/v1/sources/$source/verification", admin)

    @Test
    fun `a new source is verified, never yet, without an open job`() {
        val json = verification().json

        assertEquals(true, json.path("enabled").asBoolean())
        assertTrue(json.path("status").isNull)
        assertTrue(json.path("lastVerifiedAt").isNull)
        assertTrue(json.path("openJob").isNull)
    }

    @Test
    fun `verification is turned off and the source shows it`() {
        val off = world.api.send("PUT", "/api/v1/sources/$source/verification", admin, """{"enabled":false}""")

        assertEquals(200, off.status, off.toString())
        assertEquals(false, off.json.path("enabled").asBoolean())
        assertEquals(
            false,
            world.api
                .get("/api/v1/sources/$source", admin)
                .json
                .path("verificationEnabled")
                .asBoolean(),
        )
    }

    @Test
    fun `verify now queues a manual job of the latest whole snapshot, a second one is a conflict`() {
        snapshot()

        val queued = world.api.post("/api/v1/sources/$source/verifications", admin, "{}")

        assertEquals(202, queued.status, queued.toString())
        assertEquals(listOf("queued", "manual"), listOf("status", "reason").map { queued.json.path(it).asString() })
        assertEquals(queued.json.path("id"), verification().json.path("openJob").path("id"))
        val again = world.api.post("/api/v1/sources/$source/verifications", admin, "{}")
        assertEquals(409, again.status)
        assertEquals("verification_active", again.json.path("code").asString())
    }

    @Test
    fun `verify now of a snapshot that is not the source's, or without any, is refused`() {
        val none = world.api.post("/api/v1/sources/$source/verifications", admin, "{}")
        val unknown =
            world.api.post("/api/v1/sources/$source/verifications", admin, """{"snapshotId":"${UUID.randomUUID()}"}""")

        for (response in listOf(none, unknown)) {
            assertEquals(422, response.status, response.toString())
            assertEquals("snapshot_unusable", response.json.path("code").asString())
            assertEquals(listOf("snapshotId"), response.errorFields())
        }
    }

    @Test
    fun `the history and a record show the verification with its stages, and the source its last pass`() {
        val snapshot = snapshot()
        world.api.post("/api/v1/sources/$source/verifications", admin, "{}")
        val job = jdbc.queryForObject("select id from verification_jobs where tenant_id = ?", UUID::class.java, tenant)
        val record = UUID.randomUUID()
        val at = Timestamp.from(world.clock.now)
        jdbc.update("update verification_jobs set status = 'closed', closed_at = ? where id = ?", at, job)
        jdbc.update(
            "insert into restore_verifications (id, tenant_id, job_id, source_id, snapshot_id, key_holders, status, " +
                "level, stages, verified_at, step_id, verifier_agent_id) values (?, ?, ?, ?, ?, 2, 'passed', 5, " +
                """'[{"level":1,"name":"restic_check","state":"passed",""" +
                """"exitCode":0,"output":"ok","imageDigest":""}]', """ +
                "?, (select id from run_steps where tenant_id = ? limit 1), ?)",
            record,
            tenant,
            job,
            UUID.fromString(source),
            snapshot,
            at,
            tenant,
            verifier.agentId,
        )

        val page = world.api.get("/api/v1/sources/$source/verifications", admin).json
        val one = world.api.get("/api/v1/verifications/$record", admin).json

        assertEquals(listOf(record.toString()), page.path("items").list().map { it.path("id").asString() })
        val fields = listOf("status", "level", "keyHolders").map { one.path(it).asString() }
        assertEquals(listOf("passed", "5", "2"), fields)
        assertEquals(
            "restic_check",
            one
                .path("stages")
                .path(0)
                .path("name")
                .asString(),
        )
        assertEquals(
            "passed",
            one
                .path("stages")
                .path(0)
                .path("status")
                .asString(),
        )
        val shown = world.api.get("/api/v1/sources/$source", admin).json
        val summary = listOf("verificationStatus", "lastVerifiedLevel").map { shown.path(it).asString() }
        assertEquals(listOf("passed", "5"), summary)
        assertEquals(world.clock.now.toString(), shown.path("lastVerifiedAt").asString())
        assertEquals(404, world.api.get("/api/v1/verifications/${UUID.randomUUID()}", admin).status)
    }

    @Test
    fun `the verifiers say why each would not take the source's job`() {
        snapshot()

        val verifiers =
            world.api
                .get("/api/v1/verifiers?sourceId=$source", admin)
                .json
                .list()

        val one = verifiers.single()
        assertEquals(verifier.agentId.toString(), one.path("agentId").asString())
        assertEquals(listOf(REPOSITORY_ID), one.path("repositoryIds").list().map { it.asString() })
        assertEquals(listOf("offline", "space_unknown"), one.path("reasons").list().map { it.asString() })
        assertTrue(
            world.api
                .get("/api/v1/verifiers", admin)
                .json
                .list()
                .single()
                .path("reasons")
                .isNull,
        )
    }

    @Test
    fun `the cycle is read, set, and a wrong cron is refused by name`() {
        val set =
            world.api.send("PUT", "/api/v1/verification-schedule", admin, """{"cron":"15 3 * * *","timezone":"UTC"}""")

        assertEquals(200, set.status, set.toString())
        assertEquals(listOf("15 3 * * *", "UTC"), listOf("cron", "timezone").map { set.json.path(it).asString() })
        assertEquals(set.json, world.api.get("/api/v1/verification-schedule", admin).json)
        val wrongCron = """{"cron":"nope","timezone":"UTC"}"""
        val wrong = world.api.send("PUT", "/api/v1/verification-schedule", admin, wrongCron)
        assertEquals(422, wrong.status)
        assertEquals(listOf("cron"), wrong.errorFields())
        jdbc.update("delete from verification_schedule")
    }
}
