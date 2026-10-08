// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import org.testcontainers.containers.GenericContainer
import java.time.Duration
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * F8, the `@e2e` scenarios of docs/specs/server/ca-import.feature, "Сквозной переезд с сервера A на сервер B":
 * server A has an enrolled agent, a source with a successful run and an unused token T. The move
 * ([SardEnvironment.moveServerImportingCa]) replaces A by a server with an empty CA volume that imports the CA
 * of A; the database is the restored one (the same database, as after `pg_restore`), and the name agents dial
 * is the same. Done once, the scenarios look at the result.
 */
class CaImportMoveTest {
    private class Before(
        val agent: EnrolledAgent,
        val container: GenericContainer<*>,
        val unusedToken: EnrollmentTokens.Issued,
        val sourceId: UUID,
        val fingerprint: String,
        val startedAt: String?,
        val agentIds: List<String>,
    )

    private class Moved(
        val before: Before,
        val oldFingerprint: String,
    )

    @Test
    fun `agents of server A reconnect to server B without enrolling again`() {
        val before = moved.before
        Await.until("the agent connected to B", RECONNECT_TIMEOUT) { Backups.connected(sard, before.agent.agentId) }
        assertEquals(before.agentIds, agentIds(), "the number or the ids of the agents changed")
        assertEquals(before.startedAt, before.container.currentContainerInfo.state.startedAt, "the agent container restarted")
        assertTrue(before.container.isRunning, "the agent exited")
    }

    @Test
    fun `a backup of a source created on server A runs on server B`() {
        val before = moved.before
        Await.until("the agent connected to B", RECONNECT_TIMEOUT) { Backups.connected(sard, before.agent.agentId) }
        val started = Backups.run(sard, before.sourceId)
        val step = Backups.awaitFinished(sard, started.stepId)
        assertEquals("succeeded", step.status, step.message)
    }

    @Test
    fun `an unused token of server A registers an agent on server B`() {
        val before = moved.before
        val agent = AgentEnroller.enroll(sard, before.unusedToken.token, hostname = "h3")
        sard.track("agent-h3", AgentContainer.of(agent)).start()
        Await.until("the agent H3 connected to B", RECONNECT_TIMEOUT) { Backups.connected(sard, agent.agentId) }
        assertEquals("used", EnrollmentTokens.status(sard, before.unusedToken.id))
        assertEquals(agent.agentId, EnrollmentTokens.agentOf(sard, before.unusedToken.id))
    }

    @Test
    fun `the fingerprint in the log of server B equals the fingerprint in the token of server A`() {
        val before = moved.before
        assertEquals(before.fingerprint, before.unusedToken.token.substringAfterLast('.'))
        assertEquals(before.fingerprint, moved.oldFingerprint)
        val line = sard.serverLogs().lines().single { "origin=imported" in it }
        assertTrue("fingerprint=${before.fingerprint}" in line, line)
        assertTrue(SardEnvironment.IMPORT_DIR in line, line)
        assertEquals(before.fingerprint, ServerTls.fingerprint(ServerTls.presentedChain(sard).last()))
    }

    private fun agentIds(): List<String> =
        sard.database().use { connection ->
            connection.createStatement().use { q ->
                q.executeQuery("SELECT id FROM agents ORDER BY id").use { generateSequence { if (it.next()) it.getString(1) else null }.toList() }
            }
        }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private val RECONNECT_TIMEOUT = Duration.ofMinutes(3)

        private val moved: Moved by lazy {
            val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "h1", local = Chain.repository("/var/lib/sard-agent/repo"))
            val tree = SourceTree.generate(Chain.SEED)
            agent.host.put(Chain.DATA, tree.files())
            val container = Chain.ready(sard, agent)
            val first = Backups.start(sard, agent.agentId, "files", Chain.files(Chain.DATA))
            check(Backups.awaitFinished(sard, first.stepId).status == "succeeded") { "the backup on A failed" }
            val token = EnrollmentTokens.issue(sard)
            val fingerprint = token.token.substringAfterLast('.')
            val before =
                Before(agent, container, token, first.sourceId, fingerprint, container.currentContainerInfo.state.startedAt, agentIdsOf())
            Moved(before, sard.moveServerImportingCa())
        }

        private fun agentIdsOf(): List<String> =
            sard.database().use { c -> c.createStatement().use { q -> q.executeQuery("SELECT id FROM agents ORDER BY id").use { generateSequence { if (it.next()) it.getString(1) else null }.toList() } } }
    }
}
