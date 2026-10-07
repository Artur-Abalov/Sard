// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import org.testcontainers.containers.GenericContainer
import java.security.MessageDigest
import java.sql.ResultSet
import java.time.Duration
import java.time.Instant
import java.time.OffsetDateTime
import java.util.HexFormat
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue
import kotlin.test.fail

/**
 * S7a test 8, the ResultAck seam with the real agent: a step the agent refuses (a plugin it
 * lacks, REJECTED "unknown plugin") is recorded, rejected and its run failed, and the ResultAck
 * reaches the agent, which then stops sending the result.
 *
 * Evidence on the agent side is its executor's state dir (agent/internal/executor/store.go):
 * a result waiting for its ack is `results/<sha256(command_id)>.json`, an acknowledged one moves
 * to the tombstone `acked/<same>.json`. The agent resends a result only on a new stream, so the
 * container is restarted: without the ack it would send the result right after its Hello, and the
 * server would log it as repeated (debug).
 */
class ResultAckSeamTest {
    @Test
    fun `a rejected step is recorded, acknowledged and never sent again`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard))
        val stepId = RunRows.queueStep(sard, UUID.fromString(agent.agentId), PLUGIN)
        val container = sard.track(AgentContainer.ALIAS, AgentContainer.of(agent)).apply { start() }

        await("step $stepId rejected") { RunRows.statusOf(sard, stepId) == "rejected" }
        assertEquals("failed", runStatusOf(stepId))
        await("the agent's tombstone of $stepId") { container.has("acked", stepId) }
        assertFalse(container.has("results", stepId), "the agent still holds the result of $stepId")

        val restartedAt = databaseNow()
        container.dockerClient.restartContainerCmd(container.containerId).exec()
        await("the agent's new stream") { (lastSeenAt(agent.agentId) ?: Instant.MIN) > restartedAt }
        Thread.sleep(QUIET.toMillis())

        val repeated = "agent ${agent.agentId} repeated the result of step $stepId"
        assertFalse(repeated in sard.server.logs, "the agent sent the result again after its restart")
        assertTrue(container.has("acked", stepId))
        assertEquals(listOf("rejected", "failed"), listOf(RunRows.statusOf(sard, stepId), runStatusOf(stepId)))
    }

    /** Whether the agent's executor holds [stepId]'s file in [dir] (docker cp, as the tests never exec into the agent). */
    private fun GenericContainer<*>.has(
        dir: String,
        stepId: UUID,
    ): Boolean {
        val digest = MessageDigest.getInstance("SHA-256").digest(stepId.toString().toByteArray())
        val path = "$STATE_DIR/$dir/${HexFormat.of().formatHex(digest)}.json"
        return runCatching { copyFileFromContainer(path) { it.readAllBytes() } }.isSuccess
    }

    private fun runStatusOf(stepId: UUID): String? {
        val sql = "SELECT r.status FROM runs r JOIN run_steps s ON s.run_id = r.id WHERE s.id = ?"
        return query(sql, stepId) { it.getString(1) }
    }

    private fun lastSeenAt(agentId: String): Instant? {
        val sql = "SELECT last_seen_at FROM agents WHERE id = ?"
        return query(sql, UUID.fromString(agentId)) { it.getObject(1, OffsetDateTime::class.java)?.toInstant() }
    }

    private fun databaseNow(): Instant =
        sard.database().use { connection ->
            connection.createStatement().use { statement ->
                statement.executeQuery("SELECT now()").use {
                    it.next()
                    it.getObject(1, OffsetDateTime::class.java).toInstant()
                }
            }
        }

    private fun <T> query(
        sql: String,
        id: UUID,
        read: (ResultSet) -> T,
    ): T? =
        sard.database().use { connection ->
            connection.prepareStatement(sql).use { query ->
                query.setObject(1, id)
                query.executeQuery().use { if (it.next()) read(it) else null }
            }
        }

    private fun await(
        what: String,
        condition: () -> Boolean,
    ) {
        val deadline = Instant.now() + TIMEOUT
        while (Instant.now() < deadline) {
            if (condition()) return
            Thread.sleep(POLL.toMillis())
        }
        fail("no $what within $TIMEOUT")
    }

    companion object {
        /** A plugin no agent has (built in are postgresql, mysql, files and network). */
        private const val PLUGIN = "absent"

        /** The executor's state dir: the agent's default (agent/cmd/sard-agent/main.go). */
        private const val STATE_DIR = "/var/lib/sard-agent/executor"

        @JvmField
        @RegisterExtension
        val sard =
            SardEnvironment(
                // StepResultReceiver logs a repeated result at debug.
                mapOf("LOGGING_LEVEL_DEV_SARD_SERVER_AGENTS_RESULTS" to "DEBUG"),
            )

        private val TIMEOUT = Duration.ofSeconds(60)
        private val POLL = Duration.ofMillis(500)

        /** Long enough for a result sent right after the new stream's Hello to be handled. */
        private val QUIET = Duration.ofSeconds(5)
    }
}
