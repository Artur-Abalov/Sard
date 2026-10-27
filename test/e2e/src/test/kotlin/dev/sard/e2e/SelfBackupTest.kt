// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.MethodOrderer
import org.junit.jupiter.api.Order
import org.junit.jupiter.api.TestMethodOrder
import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * F6, checks 2–4 on the stand with sard-self: the self-backup bound to a local repository of the built-in agent
 * backs up the database (restorable into a clean PostgreSQL, with Sard's tables) and the CA with the installation's
 * configuration (byte for byte); a database the role cannot log in to fails the database's run, the state turns to
 * failing, and a success after it brings it back. The methods share one installation and run in order.
 */
@TestMethodOrder(MethodOrderer.OrderAnnotation::class)
class SelfBackupTest {
    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment(selfAgent = true)

        private const val REPOSITORY = "self"
        private val self by lazy { SelfAgent(sard) }
        private val agentId by lazy { self.start() }
        private val api by lazy { SardApi(sard) }
        private val runs = mutableMapOf<String, Pair<UUID, UUID>>()
    }

    /** Starts both system sources and waits for both runs; returns role → (run, step) and each run's status. */
    private fun backUpNow(): Map<String, String> {
        val started = api.post("/api/v1/self-backup/runs")
        assertEquals(SardApi.HTTP_OK, started.status, started.body)
        val ids = Regex("\"role\":\"([a-z_]+)\",\"sourceId\":\"[^\"]+\",\"runId\":\"([^\"]+)\"").findAll(started.body)
        val statuses = mutableMapOf<String, String>()
        for (match in ids) {
            val run = UUID.fromString(match.groupValues[2])
            val step = Await.value("the step of run $run") { stepOf(run) }
            runs[match.groupValues[1]] = run to step
            statuses[match.groupValues[1]] = Backups.awaitFinished(sard, step, Duration.ofMinutes(3)).status
        }
        return statuses
    }

    private fun stepOf(run: UUID): UUID? =
        sard.database().use { c ->
            c.prepareStatement("SELECT id FROM run_steps WHERE run_id = ?").use { q ->
                q.setObject(1, run)
                q.executeQuery().use { if (it.next()) it.getObject(1, UUID::class.java) else null }
            }
        }

    /** The self-backup's own status: it follows the list of sources, whose last runs have a status too. */
    private fun statusOf(answer: SardApi.Answer): String = answer.body.substringAfterLast("],\"status\":\"").substringBefore('"')

    private fun snapshotOf(role: String): String =
        checkNotNull(Backups.step(sard, runs.getValue(role).second)?.snapshotId) { "the run of $role made no snapshot" }

    @Test
    @Order(1)
    fun `repo add in the sidecar, a confirmed local repository, both runs succeed`() {
        self.addLocalRepository(agentId, REPOSITORY)
        val unconfirmed = api.put("/api/v1/self-backup/repository", """{"repositoryName":"$REPOSITORY"}""")
        assertEquals(422, unconfirmed.status, unconfirmed.body)
        assertEquals("local_storage_unconfirmed", unconfirmed.field("code"))

        val bound = api.put("/api/v1/self-backup/repository", """{"repositoryName":"$REPOSITORY","confirmLocalStorage":true}""")
        assertEquals(SardApi.HTTP_OK, bound.status, bound.body)
        assertTrue("\"local_storage\"" in bound.body, bound.body)

        assertEquals(mapOf("self_database" to "succeeded", "self_keys" to "succeeded"), backUpNow())
        val state = api.get("/api/v1/self-backup")
        assertEquals("ok", statusOf(state), state.body)
        assertTrue("\"lastSuccessAt\":\"" in state.body, state.body)
    }

    @Test
    @Order(2)
    fun `the snapshots carry the run's sard tags, the database one its postgresql part`() {
        for ((role, ids) in runs) {
            val tags = self.tags(REPOSITORY, snapshotOf(role))
            assertTrue("sard.run=${ids.first}" in tags, "$role: $tags")
            assertTrue("sard.step=${ids.second}" in tags, "$role: $tags")
            assertTrue(tags.any { it.startsWith("sard.source=") }, "$role: $tags")
        }
        assertTrue("postgresql.part=database" in self.tags(REPOSITORY, snapshotOf("self_database")))
    }

    @Test
    @Order(3)
    fun `the database restores into a clean PostgreSQL with Sard's tables`() {
        val dump = self.restored(REPOSITORY, snapshotOf("self_database"), "/sard.dump")
        val target = PgServer(sard, 18, alias = "db-restore").start()
        target.put("/tmp/sard.dump", dump)
        target.sql("create database restored")
        val restore = target.exec("pg_restore", "-U", "postgres", "-d", "restored", "--no-owner", "--no-acl", "/tmp/sard.dump")
        assertEquals(0, restore.exitCode, restore.stderr)
        val live = sard.database().use { c -> c.createStatement().executeQuery("SELECT count(*) FROM agents").use { it.next(); it.getLong(1) } }
        assertEquals("$live", target.query("select count(*) from sard.agents", database = "restored"))
        val tables = target.query("select count(*) from information_schema.tables where table_name in ('sources', 'runs', 'self_backups')", database = "restored")
        assertEquals("3", tables)
    }

    @Test
    @Order(4)
    fun `the CA comes back byte for byte, the configuration too, the manual backup is left out`() {
        val keys = snapshotOf("self_keys")
        for (file in listOf("ca.key", "ca.crt")) {
            val live = sard.server.copyFileFromContainer("${SardEnvironment.PKI_DIR}/ca/$file") { it.readAllBytes() }
            assertContentEquals(live, self.restored(REPOSITORY, keys, "${SardEnvironment.PKI_DIR}/ca/$file"), file)
        }
        assertEquals(self.envFile, String(self.restored(REPOSITORY, keys, "${SelfAgent.INSTALL_DIR}/.env")))
        val paths = self.paths(REPOSITORY, keys)
        assertTrue("${SelfAgent.INSTALL_DIR}/docker-compose.yml" in paths, "$paths")
        assertTrue(paths.none { it.endsWith(".dump") }, "$paths")
    }

    @Test
    @Order(5)
    fun `a database the role cannot log in to fails its run and the state, a success brings it back`() {
        sard.database().use { it.createStatement().execute("ALTER ROLE sard_backup NOLOGIN") }

        val failed = backUpNow()
        assertEquals("failed", failed["self_database"])
        assertEquals("succeeded", failed["self_keys"])
        val failing = api.get("/api/v1/self-backup")
        assertEquals("failing", statusOf(failing), failing.body)
        assertTrue("\"failing\"" in failing.body.substringAfter("\"warnings\""), failing.body)

        sard.database().use { it.createStatement().execute("ALTER ROLE sard_backup LOGIN") }
        assertEquals(mapOf("self_database" to "succeeded", "self_keys" to "succeeded"), backUpNow())
        assertEquals("ok", statusOf(api.get("/api/v1/self-backup")))
    }
}
