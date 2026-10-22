// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.MethodOrderer
import org.junit.jupiter.api.Order
import org.junit.jupiter.api.TestMethodOrder
import org.junit.jupiter.api.extension.RegisterExtension
import org.testcontainers.containers.GenericContainer
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * V3, the `@e2e` scenarios of "Правило: Признак доходит от хоста до сервера" (docs/specs/server/restic-keys.feature):
 * a key issued or removed on a host with `sudo sard-agent repo key add|remove` changes the flag the server shows within
 * 60 seconds. A container has no systemd, so the commands run with `--no-restart`, as `repo add` does on the stand,
 * and the test restarts the agent's container where the operator would run `systemctl restart sard-agent`. The
 * methods share one host and run in order: the removal needs the key the first one issued.
 */
@TestMethodOrder(MethodOrderer.OrderAnnotation::class)
class HostRecoveryKeyTest {
    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        private const val NAME = Chain.REPOSITORY
        private const val KEY_FILE = "${AgentHost.STATE_DIR}/recovery.key"
        private val api by lazy { SardApi(sard) }
        private val host by lazy {
            AgentHost(sard, "key-host", Chain.repository("${AgentHost.STATE_DIR}/repo"), persistentEtc = true)
        }
        private val agent by lazy { EnrolledAgent(AgentEnroller.enroll(host, EnrollmentTokens.create(sard)), host) }
        private val service: GenericContainer<*> by lazy { Chain.ready(sard, agent) }
    }

    @Test
    @Order(1)
    fun `a recovery key issued on the host turns the flag to present within 60 seconds`() {
        service
        RecoveryKeys.awaitFlag(api, agent.agentId, NAME, "absent")

        val add = repoKey("add", "--for", "recovery", "--to-file", KEY_FILE, "--no-restart", NAME)
        assertEquals(0, add.code, add.stderr)
        restartAgent()

        RecoveryKeys.awaitFlag(api, agent.agentId, NAME, "present")
    }

    @Test
    @Order(2)
    fun `removing the recovery key on the host turns the flag back to absent within 60 seconds`() {
        val list = host.run(AgentImage.AGENT_BINARY, "repo", "key", "list", "--json", "--config", AgentHost.CONFIG, NAME)
        assertEquals(0, list.code, list.stderr)
        val id = RecoveryKeys.recoveryIds(Json.parse(list.stdout)["keys"]).single()

        val remove = repoKey("remove", "--yes", "--no-restart", NAME, id)
        assertEquals(0, remove.code, remove.stderr)
        restartAgent()

        RecoveryKeys.awaitFlag(api, agent.agentId, NAME, "absent")
    }

    /** `sudo sard-agent repo key <verb> --config ... <args>`: flags before the names, as the agent reads them. */
    private fun repoKey(
        verb: String,
        vararg args: String,
    ) = host.run(AgentImage.AGENT_BINARY, "repo", "key", verb, "--config", AgentHost.CONFIG, *args, asRoot = true)

    private fun restartAgent() {
        service.dockerClient.restartContainerCmd(service.containerId).exec()
    }
}
