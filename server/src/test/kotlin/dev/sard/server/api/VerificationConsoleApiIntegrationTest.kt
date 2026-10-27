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

private typealias Rec = VerificationSeed.Record

private val FIELDS = listOf("rule", "source", "copy", "outcome")
private val ATTENTION = listOf("sourceId", "sourceName", "status", "lastVerifiedAt", "lastVerifiedLevel")
private val REPORT_ITEM = listOf("status", "level", "verifierHostname", "durationMs", "restoreDurationMs")
private val COUNTERS = listOf("verificationsDone", "infrastructureFailures")

/** V6: what the server gives the console to show a verification (docs/specs/server/verification-console.feature). */
@RestApiTest
class VerificationConsoleApiIntegrationTest(
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

    @Test
    fun `a mismatch carries its kind and subject`() {
        val source = seed.source("db-main", agent)
        val mismatches =
            """[{"name":"rows:public.orders","expected":"1204311","actual":"1204310"},""" +
                """{"name":"tables","expected":"42","actual":"41"}]"""
        val record = seed.record(source, agent, Rec("failed", now, mismatches = mismatches))

        val shown = verification(record)

        assertEquals(listOf("rows", "tables"), shown.pluck("mismatches", "kind"))
        assertEquals(listOf("public.orders", ""), shown.pluck("mismatches", "subject"))
    }

    @Test
    fun `a record shows the fingerprint of the source and of the copy side by side`() {
        val source = seed.source("db-main", agent)
        val facts =
            listOf(
                SeedFact("tables", "exact", "42"),
                SeedFact("rows:public.orders", "exact", "1204311"),
                SeedFact("database_size", "info", "900"),
            )
        val snapshot = seed.snapshot(source, agent, fingerprint = fingerprintOf(facts))
        val mismatches = """[{"name":"rows:public.orders","expected":"1204311","actual":"1204310"}]"""
        val record =
            seed.record(source, agent, Rec("failed", now, snapshot = snapshot, mismatches = mismatches))

        val shown = verification(record)

        val rows = shown.path("fingerprint").list()
        assertEquals(listOf("tables", "rows:public.orders", "database_size"), rows.map { it.path("name").asString() })
        assertEquals(
            listOf(
                listOf("exact", "42", "42", "matched"),
                listOf("exact", "1204311", "1204310", "mismatched"),
                listOf("info", "900", null, "not_compared"),
            ),
            rows.map { row -> FIELDS.map { row.path(it).takeUnless(JsonNode::isNull)?.asString() } },
        )
        assertEquals(0, shown.path("hiddenFacts").asInt())
    }

    @Test
    fun `a snapshot without a fingerprint gives an empty fingerprint`() {
        val source = seed.source("db-main", agent)
        val record = seed.record(source, agent, Rec("passed", now, level = 4))

        val shown = verification(record)

        assertEquals(emptyList(), shown.path("fingerprint").list())
        assertEquals(0, shown.path("hiddenFacts").asInt())
    }

    @Test
    fun `the hashed sample of files is counted, not listed`() {
        val source = seed.source("files-www", agent)
        val sample = (1..100).map { SeedFact("sha256:/srv/www/f$it", "exact", "ab$it") }
        val facts = sample + SeedFact("files", "exact", "1000")
        val snapshot = seed.snapshot(source, agent, fingerprint = fingerprintOf(facts))
        val record = seed.record(source, agent, Rec("passed", now, level = 4, snapshot = snapshot))

        val shown = verification(record)

        assertEquals(listOf("files"), shown.pluck("fingerprint", "name"))
        assertEquals(100, shown.path("hiddenFacts").asInt())
    }

    private fun theVerifier() =
        world.api
            .get("/api/v1/verifiers", admin)
            .json
            .list()
            .single { it.path("agentId").asString() == verifier.agentId.toString() }

    @Test
    fun `a verifier shows the version of its sandbox, null when unknown`() {
        assertTrue(theVerifier().path("sandboxVersion").isNull)

        jdbc.update(
            "update agents set verify_sandbox = 'podman', verify_sandbox_version = '5.2.2' where id = ?",
            verifier.agentId,
        )

        val shown = theVerifier()
        assertEquals(listOf("podman", "5.2.2"), listOf("sandbox", "sandboxVersion").map { shown.path(it).asString() })
    }

    @Test
    fun `a new verifier has done nothing and failed nothing`() {
        val shown = theVerifier()

        assertEquals(listOf(0, 0), COUNTERS.map { shown.path(it).asInt() })
        assertTrue(shown.has("verificationsDone") && shown.has("infrastructureFailures"), shown.toString())
    }

    @Test
    fun `the counters of a verifier are the records of the last 30 days and its own failures among them`() {
        val source = seed.source("db-main", agent)

        fun record(
            status: String,
            daysAgo: Long,
        ): UUID {
            val at = now.minus(Duration.ofDays(daysAgo)).plusSeconds(60)
            return seed.record(source, agent, Rec(status, at))
        }
        repeat(7) { record("passed", 1) }
        repeat(2) { record("infrastructure_failure", 2) }
        repeat(3) { record("failed", 3) }
        repeat(5) { record("passed", 40) }
        record("no_verifier", 1)

        val shown = theVerifier()

        assertEquals(listOf(12, 2), COUNTERS.map { shown.path(it).asInt() })
    }

    @Test
    fun `a failed backup and a verification that could not run are not the verifier's fault`() {
        val source = seed.source("db-main", agent)
        seed.record(source, agent, Rec("failed", now.minusSeconds(100)))
        seed.record(source, agent, Rec("cannot_verify", now.minusSeconds(200)))

        val shown = theVerifier()

        assertEquals(listOf(2, 0), COUNTERS.map { shown.path(it).asInt() })
    }

    private fun hintsOf(
        source: UUID,
        plugin: String,
        config: String,
    ): List<String> {
        jdbc.update("update sources set plugin = ?, config = cast(? as jsonb) where id = ?", plugin, config, source)
        return world.api
            .get("/api/v1/sources/$source/verification", admin)
            .json
            .path("hints")
            .list()
            .map { it.asString() }
    }

    @Test
    fun `a source's verification hints what its config lacks, off or on`() {
        val source = seed.source("db-main", agent)

        assertEquals(listOf("key_tables_missing"), hintsOf(source, "postgresql", """{"database":"app"}"""))
        assertEquals(emptyList(), hintsOf(source, "postgresql", """{"key_tables":["public.orders"]}"""))
        assertEquals(listOf("control_paths_missing"), hintsOf(source, "files", """{"paths":["/srv"]}"""))
        assertEquals(emptyList(), hintsOf(source, "files", """{"control_paths":["/srv/www"]}"""))
        jdbc.update("update sources set verification_enabled = false where id = ?", source)
        assertEquals(listOf("key_tables_missing"), hintsOf(source, "postgresql", "{}"))
    }

    private fun schedule() = world.api.get("/api/v1/verification-schedule", admin).json

    private fun save(body: String) = world.api.send("PUT", "/api/v1/verification-schedule", admin, body)

    @Test
    fun `the threshold is two days until it is saved`() {
        assertEquals(2, schedule().path("staleAfterDays").asInt())
    }

    @Test
    fun `a saved threshold is returned with the cycle, for every tenant`() {
        val saved = save("""{"cron":"30 4 * * *","timezone":"Europe/Moscow","staleAfterDays":3}""")

        assertEquals(200, saved.status, saved.toString())
        val other = world.admin(world.tenant())
        val shown = world.api.get("/api/v1/verification-schedule", other).json
        assertEquals(
            listOf("30 4 * * *", "Europe/Moscow", "3"),
            listOf("cron", "timezone", "staleAfterDays").map { shown.path(it).asString() },
        )
    }

    @Test
    fun `a threshold out of range is refused at its field and nothing changes`() {
        save("""{"cron":"30 4 * * *","timezone":"UTC","staleAfterDays":4}""")

        for (value in listOf("0", "-1", "91", "1.5")) {
            val refused = save("""{"cron":"15 3 * * *","timezone":"UTC","staleAfterDays":$value}""")
            assertEquals(422, refused.status, "$value: $refused")
            assertEquals(listOf("staleAfterDays"), refused.errorFields(), value)
        }
        assertEquals(listOf("30 4 * * *", "4"), listOf("cron", "staleAfterDays").map { schedule().path(it).asString() })
    }

    @Test
    fun `the bounds of the threshold are accepted`() {
        for (value in listOf(1, 90)) {
            val saved = save("""{"cron":"30 4 * * *","timezone":"UTC","staleAfterDays":$value}""")

            assertEquals(200, saved.status, saved.toString())
            assertEquals(value, saved.json.path("staleAfterDays").asInt())
        }
    }

    @Test
    fun `saving the cycle without a threshold leaves the threshold`() {
        save("""{"cron":"30 4 * * *","timezone":"UTC","staleAfterDays":3}""")

        val saved = save("""{"cron":"15 3 * * *","timezone":"UTC"}""")

        assertEquals(listOf("15 3 * * *", "3"), listOf("cron", "staleAfterDays").map { saved.json.path(it).asString() })
    }

    @Test
    fun `two saves at once leave one of the two pairs whole`() {
        val bodies =
            listOf(
                """{"cron":"30 4 * * *","timezone":"UTC","staleAfterDays":3}""" to ("30 4 * * *" to 3),
                """{"cron":"15 3 * * *","timezone":"UTC","staleAfterDays":5}""" to ("15 3 * * *" to 5),
            )

        repeat(5) {
            bodies.map { (body, _) -> thread { save(body) } }.forEach(Thread::join)
            val now = schedule().let { it.path("cron").asString() to it.path("staleAfterDays").asInt() }
            assertTrue(now in bodies.map { it.second }, "$now")
        }
    }

    private fun overview(session: ApiSession = admin) =
        world.api
            .get("/api/v1/overview", session)
            .json
            .path("verification")

    private fun figures(json: JsonNode = overview()) =
        listOf("enabled", "verifiedLastDay", "failed", "notHeld", "stale", "neverVerified", "disabled").map {
            it to json.path(it).asInt()
        }

    private fun record(
        source: UUID,
        status: String,
        ago: Duration,
        level: Int? = null,
    ) = seed.record(
        source,
        agent,
        Rec(status, now - ago, level = level ?: if (status == "passed") 5 else 0),
    )

    @Test
    fun `the overview counts the sources by the outcome of their last verification`() {
        record(seed.source("a", agent), "passed", Duration.ofHours(3))
        record(seed.source("b", agent), "failed", Duration.ofHours(3))
        record(seed.source("c", agent), "cannot_verify", Duration.ofHours(3))
        record(seed.source("d", agent), "infrastructure_failure", Duration.ofHours(3))
        seed.snapshot(seed.source("e", agent), agent, now.minusSeconds(3600))
        seed.source("f", agent, enabled = false)

        assertEquals(
            listOf(
                "enabled" to 5,
                "verifiedLastDay" to 1,
                "failed" to 1,
                "notHeld" to 2,
                "stale" to 0,
                "neverVerified" to 1,
                "disabled" to 1,
            ),
            figures(),
        )
    }

    @Test
    fun `a deleted source is in no figure, and another tenant's sources are not seen`() {
        val gone = seed.source("gone", agent)
        record(gone, "failed", Duration.ofHours(1))
        jdbc.update("update sources set deleted_at = ? where id = ?", java.sql.Timestamp.from(now), gone)
        val stranger = world.tenant()
        jdbc.update("update tenants set name = name where id = ?", stranger)

        assertEquals(List(7) { 0 }, figures().map { it.second })
        assertEquals(List(7) { 0 }, figures(overview(world.admin(stranger))).map { it.second })
        assertEquals(emptyList(), overview().path("attention").list())
    }

    @Test
    fun `open episodes are the stale sources, closed ones are not`() {
        val open = seed.source("open", agent)
        val closed = seed.source("closed", agent)
        for ((source, closedAt) in listOf(open to null, closed to java.sql.Timestamp.from(now))) {
            jdbc.update(
                "insert into verification_stale_episodes (id, tenant_id, source_id, counted_from, opened_at, " +
                    "closed_at, threshold_days, timezone) values (?, ?, ?, ?, ?, ?, 2, 'UTC')",
                UUID.randomUUID(),
                tenant,
                source,
                java.sql.Timestamp.from(now.minus(Duration.ofDays(3))),
                java.sql.Timestamp.from(now.minus(Duration.ofDays(1))),
                closedAt,
            )
        }

        assertEquals(1, overview().path("stale").asInt())
    }

    @Test
    fun `an attention item carries the metric of its source`() {
        val source = seed.source("db-main", agent)
        record(source, "passed", Duration.ofHours(5), level = 5)
        record(source, "cannot_verify", Duration.ofHours(1))

        val item = overview().path("attention").list().single()

        assertEquals(
            listOf(source.toString(), "db-main", "cannot_verify", now.minus(Duration.ofHours(5)).toString(), "5"),
            ATTENTION.map { item.path(it).asString() },
        )
    }

    @Test
    fun `the overview names the worst sources first and no more than ten`() {
        repeat(12) { record(seed.source("ok%02d".format(it), agent), "passed", Duration.ofHours(2L + it)) }
        val failed = seed.source("failed", agent)
        record(failed, "failed", Duration.ofHours(1))

        val listed = overview().path("attention").list()

        assertEquals(10, listed.size)
        assertEquals(failed.toString(), listed.first().path("sourceId").asString())
        assertEquals(13, overview().path("enabled").asInt())
    }

    @Test
    fun `there is no verifier when none is left, or when the only one is revoked, not when it is offline`() {
        seed.source("db-main", agent)
        assertEquals(false, overview().path("noVerifier").asBoolean(), "the verifier is registered, though offline")

        jdbc.update("update agents set revoked_at = ? where id = ?", java.sql.Timestamp.from(now), verifier.agentId)
        assertEquals(true, overview().path("noVerifier").asBoolean(), "revoked")

        jdbc.update("update sources set verification_enabled = false where tenant_id = ?", tenant)
        assertEquals(false, overview().path("noVerifier").asBoolean(), "nothing to verify")
    }

    @Test
    fun `the sources of the self-backup carry the verification metric like any other`() {
        val source = seed.source("sard-database", agent)
        jdbc.update("update sources set system_role = 'self_database' where id = ?", source)
        record(source, "passed", Duration.ofHours(1), level = 4)

        val listed =
            world.api
                .get("/api/v1/sources", admin)
                .json
                .path("items")
                .list()
                .single { it.path("id").asString() == source.toString() }

        assertEquals(
            listOf(now.minus(Duration.ofHours(1)).toString(), "4"),
            listOf("lastVerifiedAt", "lastVerifiedLevel").map { listed.path(it).asString() },
        )
    }

    private fun setup(session: ApiSession = admin) = world.api.get("/api/v1/verification-setup", session).json

    private fun web(): TestAgent =
        world.agent(
            tenant,
            snapshotOf(repositories = listOf("qa", "www").map { RepositoryEntry(it, "local", "", "") }),
            hostname = "web1.example.com",
        )

    @Test
    fun `the setup names a key command for each repository of the sources`() {
        val www = web()
        seed.source("db-main", agent)
        jdbc.update("update sources set repository_name = 'pg' where tenant_id = ?", tenant)
        seed.source("files-www", www)
        jdbc.update("update sources set repository_name = 'www' where name = 'files-www' and tenant_id = ?", tenant)

        val shown = setup()

        assertEquals(
            listOf(
                Triple("db1.example.com", "pg", "sudo sard-agent repo key add pg --for verifier"),
                Triple("web1.example.com", "www", "sudo sard-agent repo key add www --for verifier"),
            ),
            shown.path("repositories").list().map {
                val (host, name, command) =
                    listOf("sourceHostname", "repositoryName", "keyAddCommand").map { key ->
                        it.path(key).asString()
                    }
                Triple(host, name, command)
            },
        )
        assertEquals("verifier", shown.path("tokenRole").asString())
        assertTrue(shown.path("verifierRepoAdd").asString().startsWith("sudo sard-agent repo add"))
        assertTrue(shown.path("verifierRepoAdd").asString().let { "<address>" in it && "<file>" in it })
        assertTrue(shown.path("readOnlyNote").asString().contains("read"), shown.toString())
    }

    @Test
    fun `two sources of one agent in one repository give one command, a source with verification off none`() {
        seed.source("db-main", agent)
        seed.source("db-logs", agent)
        val off = seed.source("db-off", agent, enabled = false)
        jdbc.update("update sources set repository_name = 'other' where id = ?", off)

        val repositories = setup().path("repositories").list()

        assertEquals(listOf("qa"), repositories.map { it.path("repositoryName").asString() })
    }

    @Test
    fun `an installation without sources has an empty list and still the template`() {
        val shown = setup()

        assertEquals(emptyList(), shown.path("repositories").list())
        assertTrue(shown.has("verifierRepoAdd") && shown.has("tokenRole"))
    }

    @Test
    fun `the setup carries no secret of the source or of its repository`() {
        val source = seed.source("db-main", agent)
        val config = """{"password":"QA-SECRET-MARKER"}"""
        jdbc.update("update sources set config = cast(? as jsonb) where id = ?", config, source)

        val response = world.api.get("/api/v1/verification-setup", admin)

        // The word «password» occurs only as the flag name --password-from-file in the repo add template (the spec).
        val body = response.body.replace(response.json.path("verifierRepoAdd").asString(), "")
        for (marker in listOf("QA-SECRET-MARKER", "AWS_SECRET_ACCESS_KEY", "password", "secret")) {
            assertTrue(marker !in body, "$marker in $body")
        }
    }

    private fun utc() = save("""{"cron":"30 4 * * *","timezone":"UTC"}""")

    private fun report(
        source: UUID,
        query: String = "",
    ) = world.api.get("/api/v1/sources/$source/verification-report$query", admin)

    private fun day(text: String): Instant = Instant.parse("${text}T12:00:00Z")

    private fun onDay(
        source: UUID,
        status: String,
        text: String,
        snapshot: UUID? = null,
        stages: String = "[]",
        restoreMillis: Long? = null,
    ) = seed.record(
        source,
        agent,
        Rec(status, day(text), snapshot = snapshot, stages = stages, restoreMillis = restoreMillis),
    )

    @Test
    fun `the report lists the records of the period, newest first, with what a reader needs`() {
        utc()
        val source = seed.source("db-main", agent)
        val stages =
            """[{"level":1,"name":"restic_check","state":"passed","startedAt":"2026-10-20T11:58:30Z",""" +
                """"finishedAt":"2026-10-20T12:00:00Z","exitCode":0,"output":"ok","imageDigest":""}]"""
        for (text in listOf("2026-09-28", "2026-10-05", "2026-10-25")) onDay(source, "passed", text)
        val twentieth = onDay(source, "passed", "2026-10-20", stages = stages, restoreMillis = 252_000)

        val shown = report(source, "?from=2026-10-01&to=2026-10-21").json

        assertEquals(listOf("2026-10-20T12:00:00Z", "2026-10-05T12:00:00Z"), shown.pluck("items", "verifiedAt"))
        val first = shown.path("items").path(0)
        assertEquals(twentieth.toString(), first.path("id").asString())
        assertEquals(
            listOf("passed", "5", "vrf1.example.com", "90000", "252000"),
            REPORT_ITEM.map { first.path(it).asString() },
        )
        assertTrue(first.path("resticSnapshotId").asString().startsWith("4f3a9c1e7b2d"))
        assertEquals(
            listOf("2026-10-01T00:00:00Z", "2026-10-22T00:00:00Z", "UTC"),
            listOf("from", "to", "timezone").map { shown.path(it).asString() },
        )
    }

    @Test
    fun `without a period the report covers the last 30 days up to now`() {
        val source = seed.source("db-main", agent)

        val shown = report(source).json

        assertEquals(now.minus(Duration.ofDays(30)).toString(), shown.path("from").asString())
        assertEquals(now.toString(), shown.path("to").asString())
    }

    @Test
    fun `the report carries its date and the version of the server`() {
        val source = seed.source("db-main", agent)

        val shown = report(source).json

        assertEquals(now.toString(), shown.path("generatedAt").asString())
        assertEquals(
            world.api
                .get("/api/v1/status", admin)
                .json
                .path("version")
                .asString(),
            shown.path("serverVersion").asString(),
        )
        assertEquals("db-main", shown.path("sourceName").asString())
        assertEquals(false, shown.path("sourceDeleted").asBoolean())
    }

    @Test
    fun `the summary of the report counts the outcomes`() {
        utc()
        val source = seed.source("db-main", agent)
        val snapshot = seed.snapshot(source, agent, day("2026-10-01"))
        repeat(20) { onDay(source, "passed", "2026-10-10", snapshot) }
        repeat(2) { onDay(source, "failed", "2026-10-11", snapshot) }
        onDay(source, "cannot_verify", "2026-10-12", snapshot)
        onDay(source, "no_verifier", "2026-10-13", snapshot)

        val summary = report(source, "?from=2026-10-01&to=2026-10-31").json.path("summary")

        assertEquals(
            listOf(24, 20, 2, 2),
            listOf("total", "passed", "failed", "notHeld").map { summary.path(it).asInt() },
        )
    }

    @Test
    fun `a failure is shown with the pass that fixed it, an unfixed one without`() {
        utc()
        val source = seed.source("db-main", agent)
        val snapshot = seed.snapshot(source, agent, day("2026-10-01"))
        for ((status, text) in listOf("failed" to "2026-10-10", "failed" to "2026-10-11", "passed" to "2026-10-12")) {
            onDay(source, status, text, snapshot)
        }
        onDay(source, "failed", "2026-10-20", snapshot)

        val failures = report(source, "?from=2026-10-01&to=2026-10-31").json.path("failures").list()

        assertEquals(
            listOf(
                listOf("2026-10-10T12:00:00Z", "2", "2026-10-12T12:00:00Z"),
                listOf("2026-10-20T12:00:00Z", "1", null),
            ),
            failures.map { f ->
                listOf("startedAt", "records", "fixedAt").map { f.path(it).takeUnless(JsonNode::isNull)?.asString() }
            },
        )
    }

    @Test
    fun `a report of an empty period has empty lists and zeros`() {
        utc()
        val source = seed.source("db-main", agent)

        val shown = report(source, "?from=2026-10-01&to=2026-10-31").json

        assertEquals(emptyList(), shown.path("items").list())
        assertEquals(emptyList(), shown.path("failures").list())
        assertEquals(0, shown.path("summary").path("total").asInt())
    }

    @Test
    fun `the report holds no output of a stage and no config of the source`() {
        utc()
        val source = seed.source("db-main", agent)
        val config = """{"paths":["/etc"],"note":"QA-CONFIG-MARKER"}"""
        jdbc.update("update sources set config = cast(? as jsonb) where id = ?", config, source)
        val stages =
            """[{"level":2,"name":"restore","state":"passed","exitCode":0,""" +
                """"output":"QA-OUTPUT-MARKER","imageDigest":""}]"""
        onDay(source, "passed", "2026-10-10", stages = stages)

        val body = report(source, "?from=2026-10-01&to=2026-10-31").body

        assertTrue("QA-OUTPUT-MARKER" !in body && "QA-CONFIG-MARKER" !in body, body)
    }

    @Test
    fun `a wrong period is refused at its field`() {
        val source = seed.source("db-main", agent)

        val cases =
            mapOf(
                "?from=2026-10-26&to=2026-10-01" to "to",
                "?from=2025-01-01&to=2026-10-26" to "from",
                "?from=yesterday" to "from",
                "?from=2026-10-01&to=soon" to "to",
            )
        for ((query, field) in cases) {
            val refused = report(source, query)
            assertEquals(422, refused.status, query)
            assertEquals(listOf(field), refused.errorFields(), query)
        }
        assertEquals(200, report(source, "?from=2025-10-25&to=2026-10-26").status)
    }

    @Test
    fun `a report of an unknown source or of another tenant's is not found`() {
        val other = world.tenant()
        val otherAgent = world.agent(other, snapshotOf(), hostname = "other")
        val foreign =
            UUID.fromString(
                world.api
                    .post("/api/v1/sources", world.admin(other), sourceJson("theirs", otherAgent.agentId))
                    .json
                    .path("id")
                    .asString(),
            )

        assertEquals(404, report(foreign).status)
        assertEquals(404, report(UUID.randomUUID()).status)
    }

    @Test
    fun `the report of a deleted source stays, marked deleted`() {
        utc()
        val source = seed.source("db-main", agent)
        onDay(source, "passed", "2026-10-10")
        jdbc.update("update sources set deleted_at = ? where id = ?", java.sql.Timestamp.from(now), source)

        val shown = report(source, "?from=2026-10-01&to=2026-10-31")

        assertEquals(200, shown.status, shown.toString())
        assertEquals(true, shown.json.path("sourceDeleted").asBoolean())
        assertEquals(
            1,
            shown.json
                .path("items")
                .list()
                .size,
        )
    }

    @Test
    fun `a thousand records are all in the report`() {
        utc()
        val source = seed.source("db-main", agent)
        val snapshot = seed.snapshot(source, agent, day("2026-10-01"))
        repeat(1000) { onDay(source, "passed", "2026-10-10", snapshot) }

        val shown = report(source, "?from=2026-10-01&to=2026-10-31").json

        assertEquals(1000, shown.path("items").list().size)
    }
}
