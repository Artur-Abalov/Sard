// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.RegisterExtension
import java.nio.ByteBuffer
import java.security.MessageDigest
import java.time.Duration
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * T3s, stand limit С3: a backup that lasts as long as its config says, whatever the data. The
 * agent image of the stand is built with the "e2e" tag and so offers the `e2e-slow` plugin
 * (agent/plugins/e2eslow): it streams [SIZE] bytes of seeded data at [RATE] bytes per second
 * through restic's stdin and reports progress like any plugin. The step lasts SIZE/RATE ±10%
 * on the server's clock (`started_at`, the first progress, to `finished_at`, the result), and
 * the restored file is the data the seed defines.
 */
class SlowStreamTest {
    @Test
    fun `a slow stream lasts size over rate, makes a snapshot and restores the seeded bytes`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard), hostname = "slow", local = LOCAL)
        val init = agent.host.repoInit(REPOSITORY, "--generate-password")
        assertEquals(0, init.code, init.stderr)
        sard.track("agent-slow", AgentContainer.of(agent)).start()
        Await.until("repository_id of $REPOSITORY in Register") { Backups.repositoryId(sard, agent.agentId, REPOSITORY) != null }

        val started = Backups.start(sard, agent.agentId, "slow", CONFIG, plugin = "e2e-slow")
        val step = Backups.awaitFinished(sard, started.stepId, Duration.ofMinutes(2))

        assertEquals("succeeded", step.status, step.message)
        assertTrue(step.started, "no progress reached the server")
        val took = duration(started.stepId)
        assertTrue(took in EXPECTED.multipliedBy(9).dividedBy(10)..EXPECTED.multipliedBy(11).dividedBy(10), "the step took $took, want $EXPECTED ±10%")
        val snapshot = assertNotNull(Backups.snapshot(sard, started.stepId), "no snapshot of the slow stream")

        val restored = agent.host.restore(REPOSITORY_URL, PASSWORD_FILE, snapshot.snapshotId, "")
        assertEquals(setOf(FILENAME), restored.keys)
        assertContentEquals(seeded(SEED, SIZE), restored.getValue(FILENAME))
    }

    /** The step's time on the server: from its first progress (`started_at`) to its result (`finished_at`). */
    private fun duration(stepId: UUID): Duration =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT started_at, finished_at FROM run_steps WHERE id = ?").use { q ->
                q.setObject(1, stepId)
                q.executeQuery().use {
                    check(it.next()) { "no step $stepId" }
                    Duration.between(it.getTimestamp(1).toInstant(), it.getTimestamp(2).toInstant())
                }
            }
        }

    /** The data of [seed] as agent/plugins/e2eslow defines it: blocks SHA-256(seed ‖ i), big-endian, cut to [size]. */
    private fun seeded(
        seed: Long,
        size: Int,
    ): ByteArray {
        val out = ByteBuffer.allocate(size + BLOCK)
        var i = 0L
        while (out.position() < size) {
            out.put(MessageDigest.getInstance("SHA-256").digest(ByteBuffer.allocate(16).putLong(seed).putLong(i++).array()))
        }
        return out.array().copyOf(size)
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private const val SIZE = 24 shl 20
        private const val RATE = 1 shl 20
        private const val CHUNK = 64 shl 10
        private const val SEED = 20261004L
        private const val BLOCK = 32
        private const val FILENAME = "e2e-slow.bin"
        private val EXPECTED: Duration = Duration.ofSeconds((SIZE / RATE).toLong())

        /** e2e-slow plugin config (agent/plugins/e2eslow/schema.json). */
        private const val CONFIG = """{"size":$SIZE,"rate":$RATE,"chunk":$CHUNK,"seed":$SEED}"""

        private const val REPOSITORY = "main"
        private const val REPOSITORY_URL = "${AgentHost.STATE_DIR}/repo"
        private const val PASSWORD_FILE = "${AgentHost.STATE_DIR}/$REPOSITORY.pass"

        private val LOCAL =
            """
            repositories:
              - name: $REPOSITORY
                url: $REPOSITORY_URL
                password_file: $PASSWORD_FILE
            """.trimIndent() + "\n"
    }
}
