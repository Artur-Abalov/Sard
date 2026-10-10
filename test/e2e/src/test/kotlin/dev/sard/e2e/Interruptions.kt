// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import com.github.dockerjava.api.model.Bind
import com.github.dockerjava.api.model.Capability
import com.github.dockerjava.api.model.ContainerNetwork
import com.github.dockerjava.api.model.HostConfig
import com.github.dockerjava.api.model.Volume
import org.testcontainers.DockerClientFactory
import org.testcontainers.containers.GenericContainer

/**
 * T3: what breaks an agent's link to the server mid-backup, done to running containers.
 *
 * - [cut] / [reconnect]: `docker network disconnect` / `connect`, both directions at once.
 * - [block] / [unblock]: one direction only, by iptables in the agent container's network
 *   namespace, run from a helper container ([FIREWALL_IMAGE], `--network container:<agent>`,
 *   `NET_ADMIN`); the agent image has no iptables. TCP data in the open
 *   direction still arrives: dropping what the server sends lets the agent's messages reach it,
 *   and the reverse. The rules live in the namespace, so a restart of the agent drops them.
 * - [restart] / [stop] / [kill] / [start]: `docker restart` / `stop` / `kill` / `start` of any container.
 * - [erase]: a path on a stopped agent's disk removed, from a helper container on its volume.
 */
internal object Interruptions {
    /**
     * The helper image: iptables, and a shell's tools for the agent's disk (Docker Hub; the cloud
     * workaround of OQ-132 pulls it via mirror.gcr.io and tags it).
     */
    const val FIREWALL_IMAGE = "nicolaka/netshoot:v0.14"

    /**
     * Which way the data between an agent and the server's gRPC port is dropped. Only segments
     * that carry data are: without the other side's bare TCP acks the open direction could send no
     * more than its congestion window (found on the first run of case 6). A bare ack is at most 80
     * bytes (IP, TCP, timestamps, SACK) and has no PSH; a TLS record is longer, and the last
     * segment of each write has PSH.
     */
    enum class Direction(
        vararg val rules: List<String>,
    ) {
        /** The agent's messages (progress, results, heartbeats) are lost; the server's commands arrive. */
        TO_SERVER(
            listOf("OUTPUT", "-p", "tcp", "--dport", "${SardEnvironment.GRPC_PORT}", "--tcp-flags", "PSH", "PSH", "-j", "DROP"),
            listOf("OUTPUT", "-p", "tcp", "--dport", "${SardEnvironment.GRPC_PORT}", "-m", "length", "--length", "81:65535", "-j", "DROP"),
        ),

        /**
         * The server's messages (commands, ResultAck) are lost; the agent's arrive.
         */
        TO_AGENT(
            listOf("INPUT", "-p", "tcp", "--sport", "${SardEnvironment.GRPC_PORT}", "--tcp-flags", "PSH", "PSH", "-j", "DROP"),
            listOf("INPUT", "-p", "tcp", "--sport", "${SardEnvironment.GRPC_PORT}", "-m", "length", "--length", "81:65535", "-j", "DROP"),
        ),
    }

    /** Takes [agent] off the environment's network: nothing passes either way until [reconnect]. */
    fun cut(
        env: SardEnvironment,
        agent: GenericContainer<*>,
    ) {
        agent.dockerClient
            .disconnectFromNetworkCmd()
            .withNetworkId(env.dockerNetwork.id)
            .withContainerId(agent.containerId)
            .withForce(true)
            .exec()
    }

    /** Puts [agent] back on the network under its [alias] (its hostname, as [AgentHost] sets it). */
    fun reconnect(
        env: SardEnvironment,
        agent: GenericContainer<*>,
        alias: String,
    ) {
        agent.dockerClient
            .connectToNetworkCmd()
            .withNetworkId(env.dockerNetwork.id)
            .withContainerId(agent.containerId)
            .withContainerNetwork(ContainerNetwork().withAliases(alias))
            .exec()
    }

    /** Drops the packets of [direction] between [agent] and the server until [unblock]. */
    fun block(
        agent: GenericContainer<*>,
        direction: Direction,
    ) = direction.rules.forEach { iptables(agent, listOf("-A") + it) }

    /** Removes every rule [block] added. */
    fun unblock(agent: GenericContainer<*>) = iptables(agent, listOf("-F"))

    /** `docker restart`: the same container, its volumes and log, a new process. */
    fun restart(container: GenericContainer<*>) {
        container.dockerClient.restartContainerCmd(container.containerId).exec()
    }

    /** `docker stop` (SIGTERM, then SIGKILL): the container stays, stopped. */
    fun stop(container: GenericContainer<*>) {
        container.dockerClient.stopContainerCmd(container.containerId).exec()
    }

    /**
     * `docker kill` (SIGKILL): the process dies at once, as in a crash or a power cut; nothing it
     * would do on SIGTERM happens. Start it again with [start].
     */
    fun kill(container: GenericContainer<*>) {
        container.dockerClient.killContainerCmd(container.containerId).exec()
    }

    /**
     * Pulls [FIREWALL_IMAGE] now, if Docker does not have it: a first [block] or [erase] would
     * otherwise pull it (hundreds of MB) inside the interval a test times (case 8).
     */
    fun prepare(container: GenericContainer<*>) {
        DockerClientFactory.instance().checkAndPullImage(container.dockerClient, FIREWALL_IMAGE)
    }

    /**
     * `docker pause`: every process is frozen, the connections stay open and nothing answers, as a
     * server that dropped off the network silently (the case for `--connect-timeout`).
     */
    fun pause(container: GenericContainer<*>) {
        container.dockerClient.pauseContainerCmd(container.containerId).exec()
    }

    /** `docker unpause` of a container paused with [pause]. */
    fun unpause(container: GenericContainer<*>) {
        container.dockerClient.unpauseContainerCmd(container.containerId).exec()
    }

    /** `docker start` of a stopped container. */
    fun start(container: GenericContainer<*>) {
        container.dockerClient.startContainerCmd(container.containerId).exec()
    }

    /**
     * Deletes [path] (under [AgentHost.STATE_DIR]) from [host]'s disk while its agent is stopped: a
     * host that lost its disk, or was reinstalled, as far as that path goes (case 8: the executor's
     * journal).
     */
    fun erase(
        agent: GenericContainer<*>,
        host: AgentHost,
        path: String,
    ) {
        require(path.startsWith(AgentHost.STATE_DIR + "/")) { "$path is not on the agent's state volume" }
        helper(agent, "rm", listOf("-rf", path)) { it.withBinds(Bind(host.state, Volume(AgentHost.STATE_DIR))) }
    }

    private fun iptables(
        agent: GenericContainer<*>,
        args: List<String>,
    ) = helper(agent, "iptables", args) { it.withNetworkMode("container:${agent.containerId}").withCapAdd(Capability.NET_ADMIN) }

    /** Runs [program] with [args] to completion in a container of [FIREWALL_IMAGE] set up by [host]; fails unless it exits 0. */
    private fun helper(
        agent: GenericContainer<*>,
        program: String,
        args: List<String>,
        host: (HostConfig) -> HostConfig,
    ) {
        val docker = agent.dockerClient
        prepare(agent)
        val labels = DockerClientFactory.DEFAULT_LABELS + (DockerClientFactory.TESTCONTAINERS_SESSION_ID_LABEL to DockerClientFactory.SESSION_ID)
        val helper =
            docker
                .createContainerCmd(FIREWALL_IMAGE)
                .withEntrypoint(program)
                .withCmd(args)
                .withLabels(labels)
                .withHostConfig(host(HostConfig.newHostConfig()))
                .exec()
                .id
        try {
            docker.startContainerCmd(helper).exec()
            val code = docker.waitContainerCmd(helper).start().awaitStatusCode()
            check(code == 0) { "$program ${args.joinToString(" ")} exited $code" }
        } finally {
            docker.removeContainerCmd(helper).withForce(true).exec()
        }
    }
}
