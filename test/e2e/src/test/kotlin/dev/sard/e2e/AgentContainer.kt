// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.apache.commons.compress.archivers.tar.TarArchiveEntry
import org.apache.commons.compress.archivers.tar.TarArchiveOutputStream
import org.testcontainers.containers.GenericContainer
import org.testcontainers.containers.wait.strategy.Wait
import org.testcontainers.images.builder.Transferable
import org.testcontainers.utility.DockerImageName

/**
 * The real sard-agent image with an enrolled agent's tls files and a minimal config, in the
 * installation's network. The tls files are put in by hand until `sard-agent enroll` (A2)
 * writes them; see [AgentEnroller].
 */
internal object AgentContainer {
    const val ALIAS = "sard-agent"
    private const val READABLE = 0b110_100_100 // 0644
    private const val OWNER_ONLY = 0b110_000_000 // 0600
    private const val AGENT_UID = 65532 // USER of test/e2e/agent/Dockerfile

    fun of(
        sard: SardEnvironment,
        agent: AgentCredentials,
    ): GenericContainer<*> =
        GenericContainer<Nothing>(DockerImageName.parse(E2e.agentImage)).apply {
            withNetwork(sard.dockerNetwork)
            withNetworkAliases(ALIAS)
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
}
