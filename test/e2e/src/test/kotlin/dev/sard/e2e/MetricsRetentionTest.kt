// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * The `@e2e` scenarios of docs/specs/server/server-metrics.feature, "После бэкапов и прохода уборки снимки на месте, а
 * метрики читаются" and "Метрики стенда не читаются через основной порт": the stand's agent makes three successful
 * backups of one source and a failing one of another, the runs are aged by shifting their times in the database (the
 * image has no fake clock, owner's answer OV8), the cleaning pass runs on its own interval and the listener inside
 * the server's container answers a scrape with the token. The image has no curl: the scrape is bash over /dev/tcp.
 */
class MetricsRetentionTest {
    private val scrapeAttempts = Duration.ofMinutes(4)

    @Test
    fun `after backups and a pass of the cleaning the snapshots stay and the metrics are readable`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "metrics", local = LOCAL)
        agent.host.put(DATA, SourceTree.generate(Chain.SEED).files())
        sard.track("agent-metrics", AgentContainer.of(agent)).apply { start() }
        Await.until("Register of the agent") { Backups.connected(sard, agent.agentId) }
        agent.host.repoInit(REPOSITORY, "--generate-password").also { assertEquals(0, it.code, it.stderr) }

        val first = Backups.start(sard, agent.agentId, "metrics-ok", Chain.files(DATA))
        assertEquals("succeeded", Backups.awaitFinished(sard, first.stepId).status)
        val api = SardApi(sard)
        repeat(2) { assertEquals("succeeded", Backups.awaitFinished(sard, Backups.run(sard, first.sourceId, api).stepId).status) }
        val failing = Backups.start(sard, agent.agentId, "metrics-failing", Chain.files("$DATA/missing"))
        assertEquals("failed", Backups.awaitFinished(sard, failing.stepId).status)

        val snapshotsPath = "/api/v1/sources/${first.sourceId}/snapshots"
        val before = api.get(snapshotsPath)
        assertEquals(SardApi.HTTP_OK, before.status, before.body)
        val deleted = scrape().counter("""sard_retention_deleted_total\{kind="run"\}""")

        age(Duration.ofDays(AGE_DAYS))

        val after = Await.value("the cleaning pass deleted the failed run", scrapeAttempts) {
            scrape().takeIf { it.counter("""sard_retention_deleted_total\{kind="run"\}""") >= deleted + 1 }
        }
        assertEquals(before.body, api.get(snapshotsPath).body, "the snapshots of the source changed")
        assertTrue(after.counter("""sard_run_duration_seconds_count\{kind="backup",status="succeeded"\}""") >= 3)
        Await.until("the source's last success in the metrics") {
            scrape().counter("""sard_source_last_backup_success_timestamp_seconds\{source_id="${first.sourceId}"\}""") > 0
        }
        assertTrue(scrape().counter("""sard_agents_online\{role="source"\}""") >= 1)
    }

    @Test
    fun `the metrics of the stand are not read through the main port`() {
        val api = SardApi(sard)
        for (path in listOf("/actuator/prometheus", "/metrics")) {
            val answer = api.get(path)
            assertTrue(answer.body.lines().none { it.startsWith("sard_") || it.startsWith("# TYPE") }, "$path: ${answer.status}")
        }
        assertEquals(SardApi.HTTP_OK, api.get("/actuator/health").status)
    }

    /** The times of every run and step, [by] earlier: what a clock that far ahead would have made of them. */
    private fun age(by: Duration) {
        val days = by.toDays()
        sard.database().use { c ->
            c.createStatement().use {
                it.executeUpdate(
                    "UPDATE run_steps SET queued_at = queued_at - interval '$days days', dispatched_at = dispatched_at - interval '$days days', " +
                        "started_at = started_at - interval '$days days', finished_at = finished_at - interval '$days days'",
                )
                it.executeUpdate(
                    "UPDATE runs SET queued_at = queued_at - interval '$days days', started_at = started_at - interval '$days days', " +
                        "finished_at = finished_at - interval '$days days'",
                )
            }
        }
    }

    /** GET /metrics of the listener, from inside the server's container, with the token. */
    private fun scrape(): Metrics {
        val script =
            "exec 3<>/dev/tcp/127.0.0.1/${E2eMetrics.PORT}; " +
                "printf 'GET /metrics HTTP/1.0\\r\\nAuthorization: Bearer ${E2eMetrics.TOKEN}\\r\\n\\r\\n' >&3; cat <&3"
        val result = sard.server.execInContainer("bash", "-c", script)
        assertEquals(0, result.exitCode, result.stderr)
        assertTrue(result.stdout.startsWith("HTTP/1.0 200") || result.stdout.startsWith("HTTP/1.1 200"), result.stdout.take(80))
        return Metrics(result.stdout)
    }

    private class Metrics(
        val text: String,
    ) {
        /** The value of the series matching [series] (a regex of name and labels), or 0 when it is not there. */
        fun counter(series: String): Double =
            Regex("^$series (\\S+)$", RegexOption.MULTILINE).find(text)?.groupValues?.get(1)?.toDouble() ?: 0.0
    }

    private object E2eMetrics {
        const val PORT = 8081
        val TOKEN: String = UUID.randomUUID().toString().replace("-", "") + UUID.randomUUID().toString().replace("-", "")
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard =
            SardEnvironment(
                serverEnv =
                    mapOf(
                        "SARD_METRICS_BIND" to "0.0.0.0",
                        "SARD_METRICS_TOKEN" to E2eMetrics.TOKEN,
                        "SARD_METRICS_STATE_INTERVAL" to "5s",
                        "SARD_RETENTION_INTERVAL" to "1m",
                    ),
            ).also { it.secret(E2eMetrics.TOKEN) }

        private const val REPOSITORY = Chain.REPOSITORY
        private const val DATA = Chain.DATA
        private const val AGE_DAYS = 200L
        private val LOCAL = Chain.repository("${AgentHost.STATE_DIR}/repo")
    }
}
