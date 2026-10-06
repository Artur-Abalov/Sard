// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import io.grpc.ChannelCredentials

/** What enrollment leaves on an agent host: its identity, key, certificate chain and the CA to trust. */
internal class AgentCredentials(
    val agentId: String,
    val keyPem: String,
    val chainPem: String,
    val caPem: String,
) {
    /** mTLS credentials presenting this agent's certificate and trusting only its CA. */
    fun channelCredentials(): ChannelCredentials =
        ServerTls.trusting(caPem).keyManager(chainPem.byteInputStream(), keyPem.byteInputStream()).build()

    override fun toString() = "AgentCredentials(agentId=$agentId)"
}

/** An agent host after a successful `sard-agent enroll`, and the agent_id the command printed. */
internal class EnrolledAgent(
    val agentId: String,
    val host: AgentHost,
) {
    override fun toString() = "EnrolledAgent(agentId=$agentId, host=${host.hostname})"
}

/**
 * Enrolls agents the way an operator does: `sard-agent enroll` on the agent host, against the
 * environment's server (A2). This is the only place tests enroll; nothing writes the tls files
 * by hand.
 */
internal object AgentEnroller {
    private val ENROLLED = Regex("^Enrolled as agent ([0-9a-f-]{36})$", RegexOption.MULTILINE)

    /** A new host named [hostname] with [local] config and [image], enrolled with [token]; fails unless enroll exits 0. */
    fun enroll(
        env: SardEnvironment,
        token: String,
        hostname: String = "e2e-agent",
        local: String = "",
        image: String = E2e.agentImage,
    ): EnrolledAgent {
        val host = AgentHost(env, hostname, local, image)
        return EnrolledAgent(enroll(host, token), host)
    }

    /** Runs `sard-agent enroll` on [host] and returns the agent_id it printed; fails unless it exits 0. */
    fun enroll(
        host: AgentHost,
        token: String,
        vararg flags: String,
    ): String {
        val exit = host.enroll(token, *flags)
        check(exit.code == 0) { "sard-agent enroll exited ${exit.code}: ${exit.stderr}" }
        return agentIdOf(exit)
    }

    /** The agent_id in the success summary of `sard-agent enroll` (agent/cmd/sard-agent/enroll_run.go). */
    fun agentIdOf(exit: AgentHost.Exit): String = checkNotNull(ENROLLED.find(exit.stdout)) { "no agent_id in the output" }.groupValues[1]
}
