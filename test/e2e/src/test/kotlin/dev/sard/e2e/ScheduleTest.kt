// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.sql.Timestamp
import java.time.Duration
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * F3a verification 8: a schedule every minute, with the real server clock and a real agent, starts
 * two runs one after the other; the second is queued only after the first finished (D6), and the
 * journal names both.
 */
class ScheduleTest {
    private class ScheduledRun(
        val id: UUID,
        val status: String,
        val queuedAt: Timestamp,
        val finishedAt: Timestamp?,
    )

    private fun scheduledRuns(sourceId: UUID): List<ScheduledRun> =
        sard.database().use { connection ->
            connection
                .prepareStatement(
                    "SELECT id, status, queued_at, finished_at FROM runs WHERE source_id = ? AND trigger = 'schedule' " +
                        "ORDER BY queued_at",
                ).use { query ->
                    query.setObject(1, sourceId)
                    query.executeQuery().use { rows ->
                        generateSequence { if (rows.next()) rows else null }
                            .map { ScheduledRun(it.getObject(1, UUID::class.java), it.getString(2), it.getTimestamp(3), it.getTimestamp(4)) }
                            .toList()
                    }
                }
        }

    @Test
    fun `a schedule every minute runs two backups in turn, the second after the first finished`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "scheduled", local = LOCAL)
        Chain.ready(sard, agent)
        agent.host.put(Chain.DATA, SourceTree.generate(Chain.SEED).files())
        val api = SardApi(sard)
        val body = """{"name":"scheduled","agentId":"${agent.agentId}","plugin":"files","repositoryName":"${Chain.REPOSITORY}","config":${Chain.files(Chain.DATA)}}"""
        val sourceId = UUID.fromString(api.created("/api/v1/sources", body).require("id"))

        val set = api.put("/api/v1/sources/$sourceId/schedule", """{"cron":"* * * * *","timezone":"UTC","enabled":true}""")
        assertEquals(SardApi.HTTP_OK, set.status)

        val two =
            Await.value("two finished scheduled runs", Duration.ofMinutes(4)) {
                scheduledRuns(sourceId).take(2).takeIf { runs -> runs.size == 2 && runs.all { it.finishedAt != null } }
            }
        api.put("/api/v1/sources/$sourceId/schedule", """{"cron":"* * * * *","timezone":"UTC","enabled":false}""")

        val (first, second) = two
        assertEquals(listOf("succeeded", "succeeded"), two.map { it.status })
        assertTrue(!second.queuedAt.before(first.finishedAt), "the second run was queued before the first finished")
        val journal = api.get("/api/v1/sources/$sourceId/schedule/fires")
        assertEquals(SardApi.HTTP_OK, journal.status)
        two.forEach { run -> assertTrue(run.id.toString() in journal.body, "the journal does not name run ${run.id}") }
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val LOCAL = Chain.repository("${AgentHost.STATE_DIR}/repo")
    }
}
