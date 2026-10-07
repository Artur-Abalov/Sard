// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.testcontainers.containers.GenericContainer
import java.security.MessageDigest
import java.sql.ResultSet
import java.sql.Timestamp
import java.time.Instant
import java.time.OffsetDateTime
import java.util.HexFormat
import java.util.UUID
import kotlin.test.assertEquals

/**
 * T3: an enrolled agent with a local repository, running and registered, whose backups are
 * `e2e-slow` steps of a chosen length ([SlowStreamTest]); [once] counts what one step left behind.
 * Its host runs the stand's agent image, the only one with `e2e-slow` (ADR 0036, 0045).
 */
internal class T3Agent private constructor(
    private val sard: SardEnvironment,
    val agent: EnrolledAgent,
    val container: GenericContainer<*>,
) {
    val once = ExactlyOnce(sard, container, agent.host, REPOSITORY_URL, PASSWORD_FILE)
    val hostname: String get() = agent.host.hostname

    /** Starts an `e2e-slow` backup of about [seconds] on a new source and returns once the step runs. */
    fun backup(seconds: Int): Backups.Started {
        val started = Backups.start(sard, agent.agentId, hostname, config(seconds), plugin = "e2e-slow")
        awaitStatus(started.stepId, "running")
        return started
    }

    /** Starts a backup without waiting for anything: the step may stay queued or dispatched. */
    fun backupNoWait(seconds: Int): Backups.Started = Backups.start(sard, agent.agentId, hostname, config(seconds), plugin = "e2e-slow")

    fun awaitStatus(
        stepId: UUID,
        status: String,
    ) = Await.until("step $stepId $status") { RunRows.statusOf(sard, stepId) == status }

    /** Whether the agent's executor holds [stepId]'s file in [dir] (store.go; docker cp, as the tests never exec into the agent). */
    fun holds(
        dir: String,
        stepId: UUID,
    ): Boolean {
        val digest = MessageDigest.getInstance("SHA-256").digest(stepId.toString().toByteArray())
        val path = "${AgentHost.EXECUTOR_DIR}/$dir/${HexFormat.of().formatHex(digest)}.json"
        return runCatching { container.copyFileFromContainer(path) { it.readAllBytes() } }.isSuccess
    }

    companion object {
        private const val RATE = 1 shl 20
        private const val CHUNK = 64 shl 10
        private const val SEED = 20261005L

        private const val REPOSITORY = "main"
        private const val REPOSITORY_URL = "${AgentHost.STATE_DIR}/repo"
        private const val PASSWORD_FILE = "${AgentHost.STATE_DIR}/$REPOSITORY.pass"

        /** The executor's journal of accepted commands (FXa, store.go). */
        const val JOURNAL_DIR = "${AgentHost.EXECUTOR_DIR}/journal"

        private val LOCAL =
            """
            repositories:
              - name: $REPOSITORY
                url: $REPOSITORY_URL
                password_file: $PASSWORD_FILE
            """.trimIndent() + "\n"

        /**
         * Enrolls [hostname], initialises its repository, starts its agent and waits for its
         * Register; the helper image of [Interruptions] is pulled here, outside any timed interval.
         */
        fun start(
            sard: SardEnvironment,
            hostname: String,
        ): T3Agent {
            val agent =
                AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = hostname, local = LOCAL, image = E2e.standAgentImage)
            val init = agent.host.repoInit(REPOSITORY, "--generate-password")
            assertEquals(0, init.code, init.stderr)
            val container = sard.track("agent-$hostname", AgentContainer.of(agent)).apply { start() }
            Await.until("repository_id of $REPOSITORY in Register") { Backups.repositoryId(sard, agent.agentId, REPOSITORY) != null }
            Interruptions.prepare(container)
            return T3Agent(sard, agent, container)
        }

        private fun config(seconds: Int) = """{"size":${seconds * RATE},"rate":$RATE,"chunk":$CHUNK,"seed":$SEED}"""
    }
}

/** T3: the server's record of steps and agents, read from its database. */
internal class StepRows(
    private val sard: SardEnvironment,
) {
    fun lostDeadline(stepId: UUID): Instant? = one("SELECT lost_deadline FROM run_steps WHERE id = ?", stepId) { instant(it) }

    fun finishedAt(stepId: UUID): Instant = checkNotNull(one("SELECT finished_at FROM run_steps WHERE id = ?", stepId) { instant(it) })

    fun runStatus(runId: UUID): String? = one("SELECT status FROM runs WHERE id = ?", runId) { it.getString(1) }

    fun registeredAfter(
        agentId: String,
        instant: Instant,
    ): Boolean =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT 1 FROM agents WHERE id = ? AND last_register_at > ?").use { q ->
                q.setObject(1, UUID.fromString(agentId))
                q.setTimestamp(2, Timestamp.from(instant))
                q.executeQuery().use { it.next() }
            }
        }

    /** The database's clock: the one `lost_deadline` and `finished_at` are written by. */
    fun now(): Instant =
        sard.database().use { connection ->
            connection.createStatement().use { statement ->
                statement.executeQuery("SELECT now()").use {
                    it.next()
                    instant(it)!!
                }
            }
        }

    private fun instant(row: ResultSet): Instant? = row.getObject(1, OffsetDateTime::class.java)?.toInstant()

    private fun <T> one(
        sql: String,
        id: UUID,
        read: (ResultSet) -> T?,
    ): T? =
        sard.database().use { connection ->
            connection.prepareStatement(sql).use { q ->
                q.setObject(1, id)
                q.executeQuery().use { if (it.next()) read(it) else null }
            }
        }
}
