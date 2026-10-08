// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * F8, `@e2e` "Сервер B с импортированным CA без базы A называет причину отказа агенту": the new server holds the
 * CA of the old one but its database has no record of the agent's certificate (the stand deletes the record, as
 * a database restored from a copy older than the enrollment lacks it). The agent is refused with CERT_UNKNOWN,
 * stops, and the server's log says why.
 */
class CaImportWithoutDatabaseTest {
    @Test
    fun `an agent whose certificate the database does not know gets CERT_UNKNOWN and the log says why`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "h1")
        sard.database().use { c -> c.createStatement().use { it.executeUpdate("DELETE FROM agent_certificates") } }

        sard.moveServerImportingCa()
        val container = sard.track("agent-h1", AgentContainer.of(agent)).apply { start() }
        Await.until("the agent stopped after the refusal", Duration.ofMinutes(3)) { !container.isRunning }

        val line = Await.value("the CERT_UNKNOWN line of the server", Duration.ofMinutes(1)) {
            sard.serverLogs().lines().firstOrNull { "reason=CERT_UNKNOWN" in it }
        }
        assertTrue(agent.agentId in line, line)
        assertTrue("tenant=${EnrollmentTokens.DEFAULT_TENANT}" in line, line)
        assertTrue("database has no record" in line, line)
        assertEquals(78L, container.currentContainerInfo.state.exitCodeLong, "the agent's exit code")
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()
    }
}
