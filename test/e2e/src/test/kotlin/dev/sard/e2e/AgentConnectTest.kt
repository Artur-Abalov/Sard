// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.apache.commons.compress.archivers.tar.TarArchiveEntry
import org.apache.commons.compress.archivers.tar.TarArchiveOutputStream
import org.junit.jupiter.api.extension.RegisterExtension
import org.testcontainers.containers.GenericContainer
import org.testcontainers.containers.wait.strategy.Wait
import org.testcontainers.images.builder.Transferable
import org.testcontainers.utility.DockerImageName
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.fail

/**
 * The real sard-agent against the real server: after enrollment it calls
 * Register (S4a) and opens the Connect stream (S5a). The server's own record
 * is the evidence: `agents.last_register_at` (Register stored the snapshot)
 * and `last_seen_at` (a message arrived on the stream).
 *
 * The tls files are put into the container by hand until `sard-agent enroll`
 * (A2) writes them; see [AgentEnroller].
 */
class AgentConnectTest {
    @Test
    fun `an enrolled agent registers and connects its stream`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard))
        sard.track(AGENT_ALIAS, agentContainer(agent)).start()

        val row = awaitConnected(UUID.fromString(agent.agentId))
        assertEquals(E2e.version, row.version)
        assertEquals("linux", row.os)
    }

    private fun agentContainer(agent: AgentCredentials): GenericContainer<*> =
        GenericContainer<Nothing>(DockerImageName.parse(E2e.agentImage)).apply {
            withNetwork(sard.dockerNetwork)
            withNetworkAliases(AGENT_ALIAS)
            // Readable by the image's non-root user; the container is thrown away with the test.
            withCopyToContainer(Transferable.of(config(), READABLE), "/etc/sard/agent.yaml")
            withCopyToContainer(Transferable.of(agent.caPem, READABLE), "/etc/sard/ca.pem")
            withCopyToContainer(Transferable.of(agent.chainPem, READABLE), "/etc/sard/agent.pem")
            withCopyToContainer(OwnedByAgent(agent.keyPem), "/etc/sard/agent.key")
            waitingFor(Wait.forLogMessage(".*connecting to ${SardEnvironment.AGENT_ENDPOINT}.*", 1))
        }

    private fun config() =
        """
        server:
          address: ${SardEnvironment.AGENT_ENDPOINT}
        tls:
          ca_file: /etc/sard/ca.pem
          cert_file: /etc/sard/agent.pem
          key_file: /etc/sard/agent.key
        """.trimIndent()

    /**
     * The key, owned by the image's non-root user and closed to everyone else: the agent refuses
     * to start with a secret file open to group or others or owned by another user (A1).
     * Transferable.of cannot set the owner, so the tar entry is written here.
     */
    private class OwnedByAgent(
        content: String,
    ) : Transferable {
        private val bytes = content.toByteArray()

        override fun getSize() = bytes.size.toLong()

        override fun getBytes() = bytes

        override fun getFileMode() = OWNER_ONLY

        override fun transferTo(
            tar: TarArchiveOutputStream,
            destination: String,
        ) {
            val entry = TarArchiveEntry(destination)
            entry.size = size
            entry.mode = fileMode
            entry.setIds(AGENT_UID, AGENT_UID)
            tar.putArchiveEntry(entry)
            tar.write(bytes)
            tar.closeArchiveEntry()
        }
    }

    private class AgentRow(
        val version: String?,
        val os: String?,
    )

    /** Polls the server's database until the agent has registered and been seen on its stream. */
    private fun awaitConnected(agentId: UUID): AgentRow {
        val deadline = Instant.now() + CONNECT_TIMEOUT
        while (Instant.now() < deadline) {
            connectedRow(agentId)?.let { return it }
            Thread.sleep(POLL.toMillis())
        }
        fail("agent $agentId did not register and connect within $CONNECT_TIMEOUT")
    }

    private fun connectedRow(agentId: UUID): AgentRow? =
        sard.database().use { connection ->
            connection
                .prepareStatement(
                    """
                    SELECT agent_version, os FROM agents
                    WHERE id = ? AND last_register_at IS NOT NULL AND last_seen_at IS NOT NULL
                    """.trimIndent(),
                ).use { query ->
                    query.setObject(1, agentId)
                    query.executeQuery().use { if (it.next()) AgentRow(it.getString(1), it.getString(2)) else null }
                }
        }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private const val AGENT_ALIAS = "sard-agent"
        private const val READABLE = 0b110_100_100 // 0644
        private const val OWNER_ONLY = 0b110_000_000 // 0600
        private const val AGENT_UID = 65532 // USER of test/e2e/agent/Dockerfile
        private val CONNECT_TIMEOUT = Duration.ofSeconds(60)
        private val POLL = Duration.ofMillis(500)
    }
}
