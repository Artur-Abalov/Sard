// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.Timeout
import org.junit.jupiter.api.extension.RegisterExtension
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse

/**
 * J1a, cancel of a run with real containers (docs/specs/server/run-cancel-deadlines.feature, rule "Сквозные
 * проверки"): the console's `POST /api/v1/runs/{id}/cancel` stops a slow backup and a restore on the agent, which
 * confirms with CANCELLED; the source is free again and a cancelled restore leaves nothing on the host.
 */
@Timeout(value = 8, unit = TimeUnit.MINUTES)
class RunCancelTest {
    private val api = SardApi(sard)

    @Test
    fun `cancelling a slow backup over REST ends it cancelled and frees the source`() {
        val stand = T3Agent.start(sard, "cancel-backup")
        val started = stand.backup(STEP_SECONDS)

        val answer = api.post("/api/v1/runs/${started.runId}/cancel")

        assertEquals(200, answer.status)
        assertEquals("cancelling", answer.field("outcome"))
        val step = Backups.awaitFinished(sard, started.stepId)
        assertEquals("cancelled", step.status, step.message)
        val run = api.get("/api/v1/runs/${started.runId}")
        assertEquals("cancelled", run.field("status"))
        assertEquals("cancelled", run.field("outcome"))
        assertEquals(201, api.post("/api/v1/sources/${started.sourceId}/runs").status)
    }

    @Test
    fun `cancelling a restore over REST rolls it back and leaves no directory on the host`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "cancel-restore", local = LOCAL)
        agent.host.put(DATA, manyFiles())
        Chain.ready(sard, agent)
        val backup = Backups.start(sard, agent.agentId, "cancel-restore", Chain.files(DATA))
        val done = Backups.awaitFinished(sard, backup.stepId)
        assertEquals("succeeded", done.status, done.message)
        val restore = Backups.restore(sard, backup.sourceId, checkNotNull(done.snapshotId), agent.agentId, """{"destination":"copy"}""")
        Await.until("the restore step runs") { RunRows.statusOf(sard, restore.stepId) == "running" }

        val answer = api.post("/api/v1/runs/${restore.runId}/cancel")

        assertEquals(200, answer.status)
        val step = Backups.awaitFinished(sard, restore.stepId)
        assertEquals("cancelled", step.status, step.message)
        val run = api.get("/api/v1/runs/${restore.runId}")
        assertEquals("cancelled", run.field("outcome"))
        assertEquals("nothing", run.field("restoreOutcome"))
        assertFalse(agent.host.exists("$RESTORE_DIR/copy"), "the restore left a directory")
        assertEquals(emptyMap(), agent.host.tree("$RESTORE_DIR/.sard-staging"), "something is left in the staging directory")
    }

    /** Enough small files that a restore takes seconds, so that the cancel lands while it runs. */
    private fun manyFiles(): TarFiles {
        val directories = (0 until DIRECTORIES).map { TarFiles.Entry("d$it", null, TarFiles.OWNER_DIR) }
        val files =
            (0 until FILES).map {
                TarFiles.Entry("d${it % DIRECTORIES}/f$it", ByteArray(FILE_BYTES) { b -> (it + b).toByte() }, TarFiles.READABLE)
            }
        return TarFiles(listOf(TarFiles.Entry("", null, TarFiles.OWNER_DIR)) + directories + files)
    }

    companion object {
        /** Long enough for the cancel to land mid-step. */
        private const val STEP_SECONDS = 60
        private const val DIRECTORIES = 100
        private const val FILES = 30_000
        private const val FILE_BYTES = 1024
        private const val DATA = Chain.DATA
        private const val RESTORE_DIR = "${AgentHost.STATE_DIR}/restore"
        private val LOCAL = Chain.repository("${AgentHost.STATE_DIR}/repo")

        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()
    }
}
