// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.Timeout
import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * J1b, step timeout with real containers (docs/specs/server/run-cancel-deadlines.feature, rule "Сквозные проверки"):
 * the SFTP storage drops off the network without a word and the agent's ssh has no keepalive, so restic waits for
 * it forever. The source's timeout is [TIMEOUT_MINUTES] minutes: the run ends failed with outcome timed_out, by
 * the agent's own deadline or, at the latest, by the server once the lost window and a tick have passed.
 */
@Timeout(value = 8, unit = TimeUnit.MINUTES)
class StepTimeoutTest {
    private val api = SardApi(sard)

    @Test
    fun `the SFTP storage off the network mid-backup ends the run failed, outcome timed_out`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "step-timeout", local = Chain.repository(sftp.url("${SftpServer.WRITABLE}/timeout")), image = E2e.standAgentImage)
        sftp.authorize(agent.host.sshClient(sftp.knownHostsLine()))
        Chain.ready(sard, agent)

        val started = Backups.start(sard, agent.agentId, agent.host.hostname, SLOW, plugin = "e2e-slow", backupTimeoutMinutes = TIMEOUT_MINUTES)
        Await.until("step ${started.stepId} streaming") { Backups.bytesProcessed(sard, started.stepId) > STREAMING }
        Interruptions.cut(sard, sftp.container)
        try {
            val step = Backups.awaitFinished(sard, started.stepId, BOUND)
            assertEquals("timed_out", step.status, step.message)
        } finally {
            Interruptions.reconnect(sard, sftp.container, SftpServer.ALIAS)
        }
        val run = api.get("/api/v1/runs/${started.runId}")
        assertEquals("failed", run.field("status"))
        assertEquals("timed_out", run.field("outcome"))
    }

    companion object {
        private const val TIMEOUT_MINUTES = 2
        private const val SECONDS = 20
        private const val RATE = 1 shl 20
        private const val STREAMING = 2L * RATE
        private const val SLOW = """{"size":${SECONDS * RATE},"rate":$RATE,"chunk":${64 shl 10},"seed":20261027}"""

        /** The timeout, the lost window (60 s), a tick (30 s) and a margin. */
        private val BOUND = Duration.ofMinutes(TIMEOUT_MINUTES.toLong()) + Duration.ofSeconds(60 + 30 + 60)

        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val sftp by lazy { SftpServer(sard).start() }
    }
}
