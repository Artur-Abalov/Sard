// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import org.testcontainers.containers.GenericContainer
import java.sql.Timestamp
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * C1 phase 3, task 8 (docs/specs/server/agent-certificate-renewal.feature, @t8, with @t1 and @t7 on the same
 * stand): the server issues certificates for PT2M (the minimum, П1), so the renewal threshold is 80 s. A source
 * agent X in a container of its own and the built-in agent sard-self live for 7 minutes, three and a half
 * lifetimes, while X backs up on a schedule every minute. One observation serves all the methods.
 */
class CertificateLifetimesTest {
    /** One certificate record of an agent, oldest first. */
    private class Certificate(
        val serial: String,
        val predecessor: String?,
        val issuedAt: Instant,
        val supersededAt: Instant?,
        val revokedAt: Instant?,
        val notAfter: Instant,
    )

    private class Observed(
        val x: EnrolledAgent,
        val xContainer: GenericContainer<*>,
        val sourceId: UUID,
        val selfId: String,
        val started: Instant,
        val consoleStates: List<String>,
        val lastConsoleNotAfter: Instant,
    )

    private fun certificates(agentId: String): List<Certificate> =
        sard.database().use { connection ->
            connection
                .prepareStatement(
                    "SELECT serial, predecessor_serial, issued_at, superseded_at, revoked_at, not_after " +
                        "FROM agent_certificates WHERE agent_id = ? ORDER BY issued_at, serial",
                ).use { query ->
                    query.setObject(1, UUID.fromString(agentId))
                    query.executeQuery().use { rows ->
                        generateSequence { if (rows.next()) rows else null }
                            .map {
                                Certificate(
                                    it.getString(1),
                                    it.getString(2),
                                    it.getTimestamp(3).toInstant(),
                                    it.getTimestamp(4)?.toInstant(),
                                    it.getTimestamp(5)?.toInstant(),
                                    it.getTimestamp(6).toInstant(),
                                )
                            }.toList()
                    }
                }
        }

    private class Run(
        val status: String,
        val startedAt: Timestamp?,
    )

    private fun scheduledRuns(sourceId: UUID): List<Run> =
        sard.database().use { connection ->
            connection
                .prepareStatement("SELECT status, started_at FROM runs WHERE source_id = ? AND trigger = 'schedule' ORDER BY queued_at")
                .use { query ->
                    query.setObject(1, sourceId)
                    query.executeQuery().use { rows ->
                        generateSequence { if (rows.next()) rows else null }.map { Run(it.getString(1), it.getTimestamp(2)) }.toList()
                    }
                }
        }

    private fun fireOutcomes(sourceId: UUID): List<String> =
        sard.database().use { connection ->
            connection
                .prepareStatement(
                    "SELECT f.outcome FROM schedule_fires f JOIN schedules s ON s.tenant_id = f.tenant_id AND s.id = f.schedule_id " +
                        "WHERE s.source_id = ?",
                ).use { query ->
                    query.setObject(1, sourceId)
                    query.executeQuery().use { rows -> generateSequence { if (rows.next()) rows.getString(1) else null }.toList() }
                }
        }

    private fun observe(): Observed {
        val api = SardApi(sard)
        val x = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "lifetimes", local = LOCAL)
        val container = Chain.ready(sard, x)
        x.host.put(Chain.DATA, SourceTree.generate(Chain.SEED).files())
        val body = """{"name":"lifetimes","agentId":"${x.agentId}","plugin":"files","repositoryName":"${Chain.REPOSITORY}","config":${Chain.files(Chain.DATA)}}"""
        val sourceId = UUID.fromString(api.created("/api/v1/sources", body).require("id"))
        val selfId = self.start()
        val started = Instant.now()
        val set = api.put("/api/v1/sources/$sourceId/schedule", """{"cron":"* * * * *","timezone":"UTC","enabled":true}""")
        assertEquals(SardApi.HTTP_OK, set.status)

        val states = mutableListOf<String>()
        var notAfter = Instant.EPOCH
        while (Instant.now() < started + OBSERVATION) {
            val card = api.get("/api/v1/agents/${x.agentId}")
            check(card.status == SardApi.HTTP_OK) { "GET agents X answered ${card.status}" }
            states += Regex("\"state\"\\s*:\\s*\"([a-z_]+)\"").find(card.body)?.groupValues?.get(1) ?: "none"
            notAfter = Instant.parse(Regex("\"notAfter\"\\s*:\\s*\"([^\"]+)\"").find(card.body)!!.groupValues[1])
            Thread.sleep(POLL.toMillis())
        }
        api.put("/api/v1/sources/$sourceId/schedule", """{"cron":"* * * * *","timezone":"UTC","enabled":false}""")
        return Observed(x, container, sourceId, selfId, started, states, notAfter)
    }

    private fun agentCard(id: String) = SardApi(sard).get("/api/v1/agents/$id").also { assertEquals(SardApi.HTTP_OK, it.status) }

    private fun assertRenewedChain(chain: List<Certificate>) {
        assertTrue(chain.size >= MIN_CERTIFICATES, "${chain.size} certificates in ${OBSERVATION.toMinutes()} minutes")
        chain.zipWithNext { previous, next -> assertEquals(previous.serial, next.predecessor, "the predecessor of ${next.serial}") }
        assertTrue(chain.dropLast(1).all { it.supersededAt != null }, "all but the last are superseded")
        assertTrue(chain.none { it.revokedAt != null }, "no certificate is revoked")
        assertEquals(chain.size, chain.map { it.serial }.toSet().size, "the serials change")
    }

    @Test
    fun `an agent lives longer than three certificate lifetimes under the same id`() {
        val seen = observed
        assertRenewedChain(certificates(seen.x.agentId))
        val card = agentCard(seen.x.agentId)
        assertEquals(seen.x.agentId, card.require("id"))
        assertEquals("online", card.require("status"))
        assertEquals("source", card.require("role"))
        val info = seen.xContainer.currentContainerInfo
        assertEquals(0, info.restartCount)
        assertTrue(info.state.running == true && info.state.finishedAt.orEmpty().startsWith("0001-01-01"), "the agent process exited once")
        assertFalse("not reconnecting" in seen.xContainer.logs, "the agent gave up reconnecting")
    }

    @Test
    fun `an agent keeps backing up through the changes of the certificate`() {
        val seen = observed
        val runs = scheduledRuns(seen.sourceId)
        assertTrue(runs.size >= MIN_RUNS, "${runs.size} scheduled runs")
        assertEquals(emptyList(), runs.map { it.status }.filter { it != "succeeded" })
        val chain = certificates(seen.x.agentId)
        // A certificate is confirmed when its predecessor is superseded; a run started after that used it or a later one.
        for (index in 1 until chain.size - 1) {
            val confirmedAt = checkNotNull(chain[index - 1].supersededAt)
            assertTrue(
                runs.any { it.status == "succeeded" && it.startedAt != null && it.startedAt.toInstant() > confirmedAt },
                "no run succeeded after the certificate ${chain[index].serial} was confirmed",
            )
        }
        assertEquals(emptyList(), fireOutcomes(seen.sourceId).filter { it != "run_created" })
    }

    @Test
    fun `the console shows no warning for an agent that renews`() {
        val seen = observed
        assertEquals(emptyList(), seen.consoleStates.filter { it == "expiring" || it == "expired" })
        assertEquals(certificates(seen.x.agentId).last().notAfter, seen.lastConsoleNotAfter)
    }

    @Test
    fun `the built-in agent lives longer than three certificate lifetimes`() {
        val seen = observed
        assertRenewedChain(certificates(seen.selfId))
        assertEquals("online", agentCard(seen.selfId).require("status"))
        val info = self.container.currentContainerInfo
        assertEquals(0, info.restartCount)
        assertTrue(info.state.finishedAt.orEmpty().startsWith("0001-01-01"), "sard-self exited once")
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment(serverEnv = mapOf("SARD_PKI_AGENT_CERTIFICATE_VALIDITY" to "PT2M"), selfAgent = true)

        private val OBSERVATION: Duration = Duration.ofMinutes(7)
        private val POLL: Duration = Duration.ofSeconds(2)
        private const val MIN_CERTIFICATES = 4
        private const val MIN_RUNS = 5
        private val LOCAL = Chain.repository("${AgentHost.STATE_DIR}/repo")
        private val self by lazy { SelfAgent(sard) }
        private val observed by lazy { observe() }

        // Companion cannot call the instance method: the observation lives here, with the stand.
        private fun observe(): Observed = CertificateLifetimesTest().observe()
    }
}
