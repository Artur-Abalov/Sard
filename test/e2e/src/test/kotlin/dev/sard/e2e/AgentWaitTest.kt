// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.Timeout
import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import java.util.UUID
import java.util.concurrent.TimeUnit
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * J1c, the wait for an offline agent with real containers (docs/specs/server/run-cancel-deadlines.feature, rule
 * "Сквозные проверки"): the agent is stopped, the source's schedule fires every minute and the wait ceiling is one
 * minute ([WAIT_MAX]). Each run that waits for the agent ends `agent_unavailable` when the next fire is due, and
 * that fire starts a new run instead of being skipped for the active one. Once the agent is started again, the run
 * waiting for it is sent and succeeds.
 */
@Timeout(value = 8, unit = TimeUnit.MINUTES)
class AgentWaitTest {
    private fun statuses(sourceId: UUID): List<String> =
        sard.database().use { connection ->
            connection
                .prepareStatement("SELECT status FROM runs WHERE source_id = ? AND trigger = 'schedule' ORDER BY queued_at")
                .use { query ->
                    query.setObject(1, sourceId)
                    query.executeQuery().use { rows -> generateSequence { if (rows.next()) rows.getString(1) else null }.toList() }
                }
        }

    @Test
    fun `an agent that is off ends the runs as agent unavailable, the schedule goes on, the agent's return runs the next one`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "agent-wait", local = LOCAL)
        val container = Chain.ready(sard, agent)
        agent.host.put(Chain.DATA, SourceTree.generate(Chain.SEED).files())
        val api = SardApi(sard)
        val body = """{"name":"agent-wait","agentId":"${agent.agentId}","plugin":"files","repositoryName":"${Chain.REPOSITORY}","config":${Chain.files(Chain.DATA)}}"""
        val sourceId = UUID.fromString(api.created("/api/v1/sources", body).require("id"))
        Interruptions.stop(container)

        val set = api.put("/api/v1/sources/$sourceId/schedule", """{"cron":"* * * * *","timezone":"UTC","enabled":true}""")
        assertEquals(SardApi.HTTP_OK, set.status)
        val three = Await.value("three scheduled runs", Duration.ofMinutes(5)) { statuses(sourceId).takeIf { it.size >= 3 } }

        assertEquals(listOf("agent_unavailable", "agent_unavailable"), three.take(2))
        val journal = api.get("/api/v1/sources/$sourceId/schedule/fires")
        assertEquals(SardApi.HTTP_OK, journal.status)
        assertTrue("skipped_active" !in journal.body, "a fire was skipped for a run that only waited for the agent")

        Interruptions.start(container)
        Await.until("a scheduled run succeeded once the agent was back", Duration.ofMinutes(4)) { "succeeded" in statuses(sourceId) }
        api.put("/api/v1/sources/$sourceId/schedule", """{"cron":"* * * * *","timezone":"UTC","enabled":false}""")
    }

    companion object {
        private const val WAIT_MAX = "1m"

        @JvmField
        @RegisterExtension
        val sard = SardEnvironment(mapOf("SARD_RUN_AGENT_WAIT_MAX" to WAIT_MAX))

        private val LOCAL = Chain.repository("${AgentHost.STATE_DIR}/repo")
    }
}
