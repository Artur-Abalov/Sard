// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RecordingStepsQueued
import dev.sard.server.runs.Runs
import dev.sard.server.runs.RunsTenant
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.SourceFingerprint
import dev.sard.server.runs.Sources
import dev.sard.server.runs.StageState
import dev.sard.server.runs.StepOutput
import dev.sard.server.runs.StepReport
import dev.sard.server.runs.StepResults
import dev.sard.server.runs.StepState
import dev.sard.server.runs.StepTransitions
import dev.sard.server.runs.VerifierVerdict
import dev.sard.server.runs.VerifyTask
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.context.annotation.Primary
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.json.JsonMapper
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val JSON = JsonMapper.builder().build()
private val REPOSITORY_ID = "c".repeat(64)
private val SOURCE_MACHINE = "1".repeat(64)
private val VERIFIER_MACHINE = "2".repeat(64)
private const val SHARED = "shared"

/** Online agents as a test sets them; the stream manager stands behind them in production. */
class TestOnlineAgents : OnlineAgents {
    val online: MutableSet<UUID> = ConcurrentHashMap.newKeySet()

    override fun online(agentId: UUID) = agentId in online
}

@TestConfiguration(proxyBeanMethods = false)
class VerificationTestConfiguration {
    @Bean
    fun testOnlineAgents() = TestOnlineAgents()

    @Bean
    @Primary
    fun testVerifierPool(online: TestOnlineAgents) = VerifierPool(online)
}

/**
 * V4 phase 2 against PostgreSQL and the real run machinery (tests 1, 2, 4, 5, 7, 9): the cycle makes one job per
 * source, the queue hands it to a fitting verifier only, an attempt's end is settled into the history, a lost one is
 * retried and the third becomes an infrastructure failure, and a backup and a verification of one source run side by
 * side. The loop's tick is called by the test; the background ticker waits an hour.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        "sard.verification.interval=1h",
        "sard.verification.default-timezone=UTC",
        "sard.scheduler.interval=1h",
        "sard.freshness.interval=1h",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class, VerificationTestConfiguration::class)
class VerificationIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired private val steps: StepTransitions,
    @Autowired private val results: StepResults,
    @Autowired private val loop: VerificationLoop,
    @Autowired private val online: TestOnlineAgents,
    @Autowired private val queued: RecordingStepsQueued,
    @Autowired private val clock: MovableClock,
    @Autowired private val verifications: Verifications,
) {
    private lateinit var tenant: RunsTenant
    private lateinit var sourceId: UUID

    @BeforeTest
    fun setUp() {
        clock.now = RUNS_NOW
        online.online.clear()
        queued.calls.clear()
        tenant = RunsTenant(jdbc).create()
        jdbc.update("update agents set machine_id = ? where id = ?", SOURCE_MACHINE, tenant.agentId)
        jdbc.update("update agent_repositories set repository_id = ? where agent_id = ?", REPOSITORY_ID, tenant.agentId)
        sourceId = sources.create(tenant.id, tenant.draft()).id
        backup(sourceId)
        queued.calls.clear()
    }

    @AfterTest
    fun tearDown() {
        val tables = listOf("verification_stale_episodes", "restore_verifications", "verification_jobs", "snapshots")
        val admin =
            dev.sard.server.TestAdmin
                .of(jdbc)
        for (table in tables) {
            admin.update("delete from $table where tenant_id = ?", tenant.id)
        }
        jdbc.update("delete from step_logs where tenant_id = ?", tenant.id)
        tenant.drop()
    }

    // --- fixtures

    /** A succeeded backup of [source] through the real results path: it leaves a snapshot row. */
    private fun backup(
        source: UUID,
        fingerprint: SourceFingerprint? = null,
    ): String {
        val run = runs.start(tenant.id, source)
        val step = run.steps.single().id
        steps.claim(tenant.id, step)
        val snapshot = "snap-${UUID.randomUUID()}"
        val output =
            StepOutput.Backup(
                snapshot,
                2_000,
                200,
                REPOSITORY_ID,
                fingerprint = fingerprint,
                totalFiles = fingerprint?.let { 1 },
            )
        results.record(tenant.id, tenant.agentId, step, StepReport(StepState.SUCCEEDED, null, output))
        return snapshot
    }

    private fun verifier(
        machine: String = VERIFIER_MACHINE,
        reported: Boolean = true,
        repositoryId: String = REPOSITORY_ID,
        plugins: String = "array['verify']",
    ): UUID {
        val id = UUID.randomUUID()
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, registered_at, role, machine_id, verifier_reported) " +
                "values (?, ?, ?, ?, 'verifier', ?, ?)",
            id,
            tenant.id,
            "verifier-$id",
            Timestamp.from(RUNS_NOW),
            machine,
            reported,
        )
        jdbc.update(
            "insert into agent_plugins (tenant_id, agent_id, name, version, config_schema, actions) " +
                "values (?, ?, 'postgresql', '0.1.0', '{}'::jsonb, $plugins)",
            tenant.id,
            id,
        )
        jdbc.update(
            "insert into agent_repositories (tenant_id, agent_id, name, backend, repository_id) " +
                "values (?, ?, ?, 's3', ?)",
            tenant.id,
            id,
            SHARED,
            repositoryId,
        )
        online.online += id
        return id
    }

    private fun count(
        sql: String,
        id: UUID,
    ): Int? = jdbc.queryForObject(sql, Int::class.java, id)

    private fun jobs(): List<Map<String, Any?>> =
        jdbc.queryForList(
            "select id, status, failed_attempts, verifier_agent_id, run_id, cast(unsuitable as text) as unsuitable " +
                "from verification_jobs where tenant_id = ? order by created_at, id",
            tenant.id,
        )

    private fun records(): List<Map<String, Any?>> =
        jdbc.queryForList(
            "select status, level, reason, key_holders, verifier_agent_id, cast(stages as text) as stages, " +
                "cast(unsuitable as text) as unsuitable, restore_duration_ms " +
                "from restore_verifications where tenant_id = ? order by verified_at, id",
            tenant.id,
        )

    /** The VERIFY step of the current attempt of the oldest open job, else of the oldest job. */
    private fun attempt(): Map<String, Any?> =
        jdbc
            .queryForList(
                "select st.id, st.agent_id, st.action, st.status, st.snapshot_id, st.repository_name, " +
                    "cast(st.verify_task as text) as task, cast(st.config as text) as config, r.trigger " +
                    "from verification_jobs j " +
                    "join runs r on r.id = j.run_id join run_steps st on st.run_id = r.id " +
                    "where j.tenant_id = ? order by j.status = 'closed', j.created_at, j.id limit 1",
                tenant.id,
            ).single()

    private fun stepId() = attempt()["id"] as UUID

    private fun report(
        verifier: UUID,
        status: StepState,
        verify: StepOutput.Verify,
    ) {
        steps.claim(tenant.id, stepId())
        results.record(tenant.id, verifier, stepId(), StepReport(status, "verifier says", verify))
    }

    private fun lose() {
        val step = stepId()
        steps.claim(tenant.id, step)
        val past = Timestamp.from(clock.now.minusSeconds(1))
        jdbc.update("update run_steps set lost_deadline = ? where id = ?", past, step)
        assertTrue(steps.lost(tenant.id, step))
    }

    private fun stage(
        level: Int,
        state: StageState = StageState.PASSED,
    ) = StepOutput.Stage(level, "stage-$level", state, null, null, 0, "", "")

    private val passed =
        StepOutput.Verify(
            "snap",
            emptyList(),
            listOf(stage(0), stage(1), stage(2), stage(3), stage(4)),
            VerifierVerdict.PASSED,
            emptyList(),
            90_000,
        )

    // --- test 1: the cycle

    @Test
    fun `the cycle makes one job per source with verification on, and a second tick makes no second one`() {
        verifier()
        val off = sources.create(tenant.id, tenant.draft(name = "off")).id
        backup(off)
        jdbc.update("update sources set verification_enabled = false where id = ?", off)
        online.online.clear()

        loop.tick()
        loop.tick()

        val jobs = jobs()
        assertEquals(1, jobs.size)
        assertEquals("queued", jobs.single()["status"])
        assertEquals(1, count("select count(*) from verification_jobs where source_id = ?", sourceId))
    }

    @Test
    fun `a tenant without any verifier gets no job and no record`() {
        loop.tick()

        assertEquals(emptyList(), jobs())
        assertEquals(emptyList(), records())
    }

    @Test
    fun `the next cycle verifies the same snapshot again when there is no newer one`() {
        val verifier = verifier()
        loop.tick()
        report(verifier, StepState.SUCCEEDED, passed)
        loop.tick()

        clock.now = Instant.parse("2026-10-01T04:30:00Z")
        loop.tick()

        assertEquals(listOf("closed", "running"), jobs().map { it["status"] })
    }

    // --- test 2: who gets the job

    @Test
    fun `a fitting verifier gets a VERIFY step of its own name for the repository, and is woken`() {
        val verifier = verifier()

        loop.tick()

        val attempt = attempt()
        assertEquals(
            listOf<Any?>(verifier, "verify", "queued", SHARED, "verification"),
            listOf("agent_id", "action", "status", "repository_name", "trigger").map { attempt[it] },
        )
        val snapshot =
            jdbc.queryForObject("select snapshot_id from snapshots where source_id = ?", String::class.java, sourceId)
        assertEquals(snapshot, attempt["snapshot_id"])
        val task = VerifyTask.of(attempt["task"] as String)!!
        assertEquals(REPOSITORY_ID, task.repositoryId)
        // Owner decision: the verifier reaches no production system, the source's config is not its business.
        assertEquals("{}", attempt["config"])
        assertEquals(Instant.parse("2026-10-01T04:30:00Z"), task.deadline)
        assertEquals(Duration.ofHours(6).seconds, task.timeoutSeconds)
        // V5, V-D7: the verifier refuses its own machine, so the task names the source's.
        assertEquals(listOf(SOURCE_MACHINE), task.sourceMachineIds)
        assertEquals(listOf(tenant.id to verifier), queued.calls)
        assertEquals(listOf("running"), jobs().map { it["status"] })
    }

    // Scenario (V2, checks 6 and 7): the job carries the fingerprint of the snapshot and its hash; a snapshot from
    // before V2 has none, and its job goes without, for the verifier to stop at level 3.
    @Test
    fun `the job carries the fingerprint of the snapshot and its hash, or none for a snapshot without one`() {
        verifier()
        loop.tick()
        val before = VerifyTask.of(attempt()["task"] as String)!!
        assertEquals(listOf<Any?>(null, null), listOf(before.fingerprint, before.fingerprintSha256))
        report(verifier = jobs().single()["verifier_agent_id"] as UUID, StepState.SUCCEEDED, passed)
        loop.tick()

        val canonical = """{"facts":[{"name":"tables","rule":"exact","value":"3"}],"format":1,"plugin":"postgresql"}"""
        val bytes = canonical.toByteArray()
        val sha256 =
            java.security.MessageDigest
                .getInstance("SHA-256")
                .digest(bytes)
                .joinToString("") { "%02x".format(it) }
        // A newer snapshot than setUp's: at the same instant the cycle's pick would depend on the ids' random bits.
        clock.now = clock.now.plusSeconds(60)
        backup(sourceId, SourceFingerprint(bytes, sha256))
        clock.now = Instant.parse("2026-10-01T04:30:00Z")
        loop.tick()

        val task = VerifyTask.of(attempt()["task"] as String)!!
        assertEquals(sha256, task.fingerprintSha256)
        assertTrue(bytes.contentEquals(task.fingerprint))
    }

    @Test
    fun `a verifier on the source's machine never gets the job, which ends as no verifier with the reason`() {
        verifier(machine = SOURCE_MACHINE)

        loop.tick()
        assertEquals(listOf("queued"), jobs().map { it["status"] })
        assertTrue("same_host" in jobs().single()["unsuitable"] as String)

        clock.now = Instant.parse("2026-10-01T04:30:00Z")
        loop.tick()

        val record = records().first()
        val told = listOf("status", "level", "reason").map { record[it] }
        assertEquals(listOf<Any?>("no_verifier", 0, "no verifier fits"), told)
        assertTrue("same_host" in record["unsuitable"] as String)
        assertEquals(
            0,
            count("select count(*) from run_steps where tenant_id = ? and action = 'verify'", tenant.id),
        )
    }

    @Test
    fun `verifiers without the key, without a verifying plugin or older than V4 do not get the job`() {
        verifier(repositoryId = "d".repeat(64))
        verifier(plugins = "array['backup']")
        verifier(reported = false)

        loop.tick()

        val unsuitable = jobs().single()["unsuitable"] as String
        for (reason in listOf("no_key", "no_plugin", "outdated")) assertTrue(reason in unsuitable, unsuitable)
        assertEquals(emptyList(), queued.calls)
    }

    // --- test 4: results

    @Test
    fun `a passed verification is recorded with its stages, level 5 and the key holders`() {
        val verifier = verifier()
        loop.tick()

        report(verifier, StepState.SUCCEEDED, passed)
        loop.tick()

        val record = records().single()
        assertEquals(
            listOf<Any?>("passed", 5, verifier, 2, 90_000L),
            listOf("status", "level", "verifier_agent_id", "key_holders", "restore_duration_ms").map { record[it] },
        )
        assertEquals(5, JSON.readTree(record["stages"] as String).size())
        assertEquals(listOf("closed"), jobs().map { it["status"] })
    }

    @Test
    fun `a failure at stage 3 is a failed verification at level 2`() {
        val verifier = verifier()
        loop.tick()
        val stages = listOf(stage(1), stage(2), stage(3, StageState.FAILED))
        val failed = passed.copy(stages = stages, verdict = VerifierVerdict.FAILED)

        report(verifier, StepState.FAILED, failed)
        loop.tick()

        val record = records().single()
        assertEquals(listOf<Any?>("failed", 2, "verifier says"), listOf("status", "level", "reason").map { record[it] })
    }

    // --- test 5: retries

    @Test
    fun `a lost attempt is tried again, and the third lost one in a row is an infrastructure failure`() {
        verifier()
        loop.tick()

        lose()
        loop.tick()
        assertEquals(listOf<Any?>("running", 1), jobs().single().let { listOf(it["status"], it["failed_attempts"]) })

        lose()
        loop.tick()
        lose()
        loop.tick()

        val record = records().single()
        assertEquals("infrastructure_failure", record["status"])
        assertTrue((record["reason"] as String).startsWith("3 attempts without a verdict; the last: lost"))
        assertEquals(
            3,
            count("select count(*) from runs where tenant_id = ? and trigger = 'verification'", tenant.id),
        )
    }

    // --- test 6: events

    private fun episodes(): List<Map<String, Any?>> =
        jdbc.queryForList(
            "select counted_from, opened_at, closed_at, closed_by_id from verification_stale_episodes " +
                "where tenant_id = ? order by opened_at, id",
            tenant.id,
        )

    @Test
    fun `a pass right after a failure is marked recovered, the first pass and the failure are not`() {
        val verifier = verifier()
        loop.tick()
        report(verifier, StepState.SUCCEEDED, passed)
        loop.tick()
        clock.now = Instant.parse("2026-10-01T04:30:00Z")
        loop.tick()
        val failed = passed.copy(stages = listOf(stage(1, StageState.FAILED)), verdict = VerifierVerdict.FAILED)
        report(verifier, StepState.FAILED, failed)
        loop.tick()
        clock.now = Instant.parse("2026-10-02T04:30:00Z")
        loop.tick()
        report(verifier, StepState.SUCCEEDED, passed)
        loop.tick()

        val marks =
            jdbc.queryForList(
                "select status, recovered from restore_verifications where tenant_id = ? order by verified_at",
                tenant.id,
            )
        assertEquals(listOf("passed", "failed", "passed"), marks.map { it["status"] })
        assertEquals(listOf(false, false, true), marks.map { it["recovered"] })
    }

    @Test
    fun `a source not verified for more than seven days opens one episode, and a pass closes it`() {
        loop.tick()
        assertEquals(emptyList(), episodes())

        clock.now = RUNS_NOW + Duration.ofDays(7) + Duration.ofSeconds(1)
        loop.tick()
        loop.tick()
        clock.now = clock.now + Duration.ofDays(1)
        loop.tick()

        val opened = episodes().single()
        assertEquals(Timestamp.from(RUNS_NOW), opened["counted_from"])
        assertEquals(null, opened["closed_at"])

        val verifier = verifier()
        loop.tick()
        report(verifier, StepState.SUCCEEDED, passed)
        loop.tick()

        val closed = episodes().single()
        assertTrue(closed["closed_at"] != null)
        val pass =
            jdbc.queryForObject("select id from restore_verifications where tenant_id = ?", UUID::class.java, tenant.id)
        assertEquals(pass, closed["closed_by_id"])
    }

    @Test
    fun `turning verification off closes an open episode without a pass`() {
        clock.now = RUNS_NOW + Duration.ofDays(8)
        loop.tick()
        jdbc.update("update sources set verification_enabled = false where id = ?", sourceId)

        loop.tick()

        val closed = episodes().single()
        assertTrue(closed["closed_at"] != null)
        assertEquals(null, closed["closed_by_id"])
    }

    // --- test 8: lastVerifiedAt, and a self-backup source like any other

    @Test
    fun `lastVerifiedAt is the oldest last pass of the verified sources, null while one has none`() {
        val verifier = verifier()
        val second = sources.create(tenant.id, tenant.draft(name = "second")).id
        backup(second)
        assertEquals(null, verifications.lastVerifiedAt(tenant.id))

        loop.tick()
        report(verifier, StepState.SUCCEEDED, passed)
        val firstPass = clock.now
        loop.tick()
        assertEquals(null, verifications.lastVerifiedAt(tenant.id), "the second source is not verified yet")

        clock.now = clock.now.plusSeconds(60)
        loop.tick()
        report(verifier, StepState.SUCCEEDED, passed)
        loop.tick()

        assertEquals(firstPass, verifications.lastVerifiedAt(tenant.id))
    }

    @Test
    fun `a self-backup source is verified the same way`() {
        jdbc.update("update sources set system_role = 'self_database' where id = ?", sourceId)
        val verifier = verifier()

        loop.tick()
        report(verifier, StepState.SUCCEEDED, passed)
        loop.tick()

        assertEquals(listOf("passed"), records().map { it["status"] })
        assertEquals(clock.now, verifications.lastVerifiedAt(tenant.id))
    }

    // --- test 7: a backup and a verification of one source side by side

    @Test
    fun `a backup starts while the source is being verified, and a verification is handed out during a backup`() {
        verifier()
        loop.tick()

        val backupRun = runs.start(tenant.id, sourceId)
        assertEquals(1, backupRun.steps.size)

        val second = sources.create(tenant.id, tenant.draft(name = "second")).id
        backup(second)
        runs.start(tenant.id, second)
        verifier()
        clock.now = clock.now.plusSeconds(1)
        loop.tick()

        val sources =
            jdbc.queryForList(
                "select source_id from verification_jobs where tenant_id = ? and status = 'running'",
                UUID::class.java,
                tenant.id,
            )
        assertEquals(setOf(sourceId, second), sources.toSet())
    }

    // --- test 9: an agent N-1 is a source as before and never a verifier

    @Test
    fun `an agent that sent no verifier status still backs up and is never handed a verification`() {
        verifier(reported = false)

        loop.tick()
        backup(sourceId)

        assertEquals(
            0,
            count("select count(*) from run_steps where tenant_id = ? and action = 'verify'", tenant.id),
        )
    }
}
