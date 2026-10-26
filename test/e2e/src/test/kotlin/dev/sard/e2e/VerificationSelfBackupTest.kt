// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import com.github.dockerjava.api.model.AccessMode
import com.github.dockerjava.api.model.Bind
import com.github.dockerjava.api.model.Volume
import org.junit.jupiter.api.extension.RegisterExtension
import java.time.Duration
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * V5, check 9: the snapshot of Sard's own database, made by the agent sard-self next to the server ([SelfAgent]), is
 * verified as any other postgresql snapshot: in a sandbox of the server's major version, to level 5. The verifier
 * reads the self-backup's local repository through the group of the service, read only.
 */
class VerificationSelfBackupTest {
    @Test
    fun `the snapshots of Sard's own database and keys pass every stage, the database in its sandbox`() {
        self.addLocalRepository(agentId, REPOSITORY)
        val bound = api.put("/api/v1/self-backup/repository", """{"repositoryName":"$REPOSITORY","confirmLocalStorage":true}""")
        assertEquals(SardApi.HTTP_OK, bound.status, bound.body)
        val snapshots = backUpNow()
        self.shareWithGroup(REPOSITORY)
        val key = self.verifierKey(REPOSITORY)
        val verifier =
            VerifierStand(sard, verifications).apply {
                val repository: (String) -> Bind = { path -> Bind("${self.statePath}/$REPOSITORY", Volume(path), AccessMode.ro) }
                enroll(listOf(VerifierStand.Shared(REPOSITORY, repository, "${AgentHost.STATE_DIR}/verifier-self.pass", key, group = SELF_GID)))
                start()
            }

        val database = verifications.verify(snapshots.getValue("self_database"))
        // The other system source, the keys and the configuration, is files; the cycle verifies it too.
        val keys = verifications.verify(snapshots.getValue("self_keys"))

        assertEquals("passed", database.status, database.reason)
        assertEquals(5, database.level)
        assertEquals("passed", keys.status, keys.reason)
        verifications.awaitIdle()
        assertEquals(emptyList(), verifications.sandboxes())
        assertEquals(emptyList(), verifier.workVolume())
    }

    /** Runs the self-backup now and returns the snapshot (snapshots.id) of each system source, by role. */
    private fun backUpNow(): Map<String, UUID> {
        val started = api.post("/api/v1/self-backup/runs")
        assertEquals(SardApi.HTTP_OK, started.status, started.body)
        val ids = Regex("\"role\":\"([a-z_]+)\",\"sourceId\":\"([^\"]+)\",\"runId\":\"([^\"]+)\"").findAll(started.body).toList()
        assertEquals(setOf("self_database", "self_keys"), ids.map { it.groupValues[1] }.toSet(), started.body)
        return ids.associate { match ->
            val run = UUID.fromString(match.groupValues[3])
            val step =
                Await.value("the step of run $run") { verifications.query("SELECT id FROM run_steps WHERE run_id = ?", run) { it.getObject(1, UUID::class.java) } }
            assertEquals("succeeded", Backups.awaitFinished(sard, step, Duration.ofMinutes(3)).status)
            match.groupValues[1] to verifications.snapshotOfStep(step)
        }
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment(selfAgent = true)

        private const val REPOSITORY = "self"

        /** The group of sard-self (the server's uid and gid, 10001). */
        private const val SELF_GID = "10001"
        private val verifications = Verifications(sard)
        private val self by lazy { SelfAgent(sard) }
        private val agentId by lazy { self.start() }
        private val api by lazy { SardApi(sard) }
    }
}
