// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.MethodOrderer
import org.junit.jupiter.api.Order
import org.junit.jupiter.api.TestMethodOrder
import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * V3, the `@e2e` scenarios of "Правило: Самобэкап выдаёт ключ восстановления" (docs/specs/server/restic-keys.feature):
 * the `recoveryKeyCommand` of the self-backup state, run as the operator runs it on the server's machine (a one-off
 * container of the sidecar's image in a terminal, then a restart of the sidecar), issues the key, and the server learns
 * the flag from the next Register; a repeat answers "unchanged". The methods share one installation and run in order.
 */
@TestMethodOrder(MethodOrderer.OrderAnnotation::class)
class SelfRecoveryKeyTest {
    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment(selfAgent = true)

        private const val REPOSITORY = "self"
        private val self by lazy { SelfAgent(sard) }
        private val agentId by lazy { self.start() }
        private val api by lazy { SardApi(sard) }
    }

    private fun command(): String {
        val state = api.get("/api/v1/self-backup")
        return assertNotNull(state.field("recoveryKeyCommand"), "the state has no recoveryKeyCommand: ${state.body}")
    }

    @Test
    @Order(1)
    fun `the command issues the key once and the server learns the flag within 60 seconds`() {
        self.addLocalRepository(agentId, REPOSITORY)
        val bound = api.put("/api/v1/self-backup/repository", """{"repositoryName":"$REPOSITORY","confirmLocalStorage":true}""")
        assertEquals(SardApi.HTTP_OK, bound.status, bound.body)
        firstBackup()
        RecoveryKeys.awaitFlag(api, agentId, REPOSITORY, "absent")

        val output = self.runRecoveryKeyCommand(command())

        val passwords = RecoveryKeys.PASSWORD.findAll(output).map { it.value }.toList()
        passwords.forEach(sard::secret)
        assertEquals(1, passwords.size, "the password of the key is shown once")
        assertEquals(1, RecoveryKeys.recoveryIds(self.keys(REPOSITORY)).size)
        RecoveryKeys.awaitFlag(api, agentId, REPOSITORY, "present")
    }

    @Test
    @Order(2)
    fun `a repeat of the command prints unchanged and leaves one recovery key`() {
        val output = self.runRecoveryKeyCommand(command())

        assertContains(output, "unchanged")
        assertEquals(1, RecoveryKeys.recoveryIds(self.keys(REPOSITORY)).size)
        RecoveryKeys.awaitFlag(api, agentId, REPOSITORY, "present")
    }

    /** The first backup of both system sources, as F6: the binding is complete only after it. */
    private fun firstBackup() {
        val started = api.post("/api/v1/self-backup/runs")
        assertEquals(SardApi.HTTP_OK, started.status, started.body)
        val runs = Regex("\"runId\":\"([^\"]+)\"").findAll(started.body).map { it.groupValues[1] }.toList()
        assertTrue(runs.isNotEmpty(), started.body)
        for (run in runs) {
            val step = Await.value("the step of run $run") { stepOf(run) }
            assertEquals("succeeded", Backups.awaitFinished(sard, step, Duration.ofMinutes(3)).status)
        }
    }

    private fun stepOf(run: String): java.util.UUID? =
        sard.database().use { c ->
            c.prepareStatement("SELECT id FROM run_steps WHERE run_id = ?").use { q ->
                q.setObject(1, java.util.UUID.fromString(run))
                q.executeQuery().use { if (it.next()) it.getObject(1, java.util.UUID::class.java) else null }
            }
        }
}
