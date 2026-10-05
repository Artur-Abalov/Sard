// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.testcontainers.containers.GenericContainer
import java.util.UUID
import kotlin.test.assertEquals

/**
 * T3: "exactly one" from three sources that do not depend on each other, for one backup step of
 * an agent [container] on [host] whose repository is [repositoryUrl]:
 *
 * - [resticBackups]: `restic backup` runs the agent logged for the step's command_id (FXa,
 *   agent/internal/restic/restic.go: `restic started command_id=… command=backup`);
 * - [taggedSnapshots]: snapshots in the repository tagged `sard.step=<command_id>` (FXs), read by
 *   restic itself, next to [snapshotRows], the server's rows for the step;
 * - [runFinished]: RunFinished events the server logged for the run (FXs,
 *   runs/RunFinishedTrace.kt: `run <id> finished: <status>`), next to `runs.finished_at`.
 *
 * The agent's log is `docker logs` of its container, every start of it included; the server's is
 * [SardEnvironment.serverLogs], every container of it included.
 */
internal class ExactlyOnce(
    private val sard: SardEnvironment,
    private val container: GenericContainer<*>,
    private val host: AgentHost,
    private val repositoryUrl: String,
    private val passwordFile: String,
) {
    /** The agent's log, every start of its container. */
    fun agentLog(): String = container.logs

    /** Lines of the agent's log that carry [message] and the step's `command_id`. */
    fun agentLines(
        message: String,
        stepId: UUID,
    ): Int = count(agentLog(), message, "command_id=$stepId")

    fun resticBackups(stepId: UUID): Int = count(agentLog(), "restic started", "command_id=$stepId", "command=backup")

    fun taggedSnapshots(stepId: UUID): Int {
        val exit =
            host.run(
                AgentImage.RESTIC_BINARY, "snapshots", "--json", "--no-cache", "--no-lock", "--tag", "${STEP_TAG}=$stepId",
                "--repo", repositoryUrl, "--password-file", passwordFile,
            )
        check(exit.code == 0) { "restic snapshots exited ${exit.code}: ${exit.stderr}" }
        return Regex("\"short_id\"").findAll(exit.stdout).count()
    }

    fun snapshotRows(stepId: UUID): Int =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT count(*) FROM snapshots WHERE step_id = ?").use { q ->
                q.setObject(1, stepId)
                q.executeQuery().use {
                    check(it.next())
                    it.getInt(1)
                }
            }
        }

    fun runFinished(runId: UUID): Int = count(sard.serverLogs(), "run $runId finished: ")

    /** `runs.finished_at` is set: the run ended, once, as the guarded update allows. */
    fun runEnded(runId: UUID): Boolean =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT finished_at IS NOT NULL FROM runs WHERE id = ?").use { q ->
                q.setObject(1, runId)
                q.executeQuery().use { it.next() && it.getBoolean(1) }
            }
        }

    /** One backup run, one snapshot (in the repository and on the server), one RunFinished. */
    fun assertOne(started: Backups.Started) {
        val step = started.stepId
        assertEquals(1, resticBackups(step), "restic backup runs of step $step in the agent's log")
        assertEquals(1, taggedSnapshots(step), "snapshots tagged $STEP_TAG=$step in the repository")
        assertEquals(1, snapshotRows(step), "snapshot rows of step $step on the server")
        assertEquals(1, runFinished(started.runId), "RunFinished of run ${started.runId} in the server's log")
        assertEquals(true, runEnded(started.runId), "runs.finished_at of run ${started.runId}")
    }

    companion object {
        /** The tag the server gives every step's snapshots (DispatchParts.kt, STEP_TAG). */
        const val STEP_TAG = "sard.step"

        /** Lines of [log] that contain every one of [parts]. */
        fun count(
            log: String,
            vararg parts: String,
        ): Int = log.lineSequence().count { line -> parts.all { it in line } }
    }
}
