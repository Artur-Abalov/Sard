// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import com.github.dockerjava.api.model.Capability
import com.github.dockerjava.api.model.ContainerNetwork
import com.github.dockerjava.api.model.HostConfig
import org.testcontainers.DockerClientFactory
import org.testcontainers.containers.GenericContainer

/**
 * T3: what breaks an agent's link to the server mid-backup, done to running containers.
 *
 * - [cut] / [reconnect]: `docker network disconnect` / `connect`, both directions at once.
 * - [block] / [unblock]: one direction only, by iptables in the agent container's network
 *   namespace, run from a helper container ([FIREWALL_IMAGE], `--network container:<agent>`,
 *   `NET_ADMIN`); the agent image is distroless and has no iptables. TCP data in the open
 *   direction still arrives: dropping what the server sends lets the agent's messages reach it,
 *   and the reverse. The rules live in the namespace, so a restart of the agent drops them.
 * - [restart] / [stop]: `docker restart` / `docker stop` of any container.
 */
internal object Interruptions {
    /** An image with iptables (Docker Hub; the cloud workaround of OQ-132 pulls it via mirror.gcr.io and tags it). */
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

    private fun iptables(
        agent: GenericContainer<*>,
        args: List<String>,
    ) {
        val docker = agent.dockerClient
        DockerClientFactory.instance().checkAndPullImage(docker, FIREWALL_IMAGE)
        val labels = DockerClientFactory.DEFAULT_LABELS + (DockerClientFactory.TESTCONTAINERS_SESSION_ID_LABEL to DockerClientFactory.SESSION_ID)
        val helper =
            docker
                .createContainerCmd(FIREWALL_IMAGE)
                .withEntrypoint("iptables")
                .withCmd(args)
                .withLabels(labels)
                .withHostConfig(
                    HostConfig.newHostConfig().withNetworkMode("container:${agent.containerId}").withCapAdd(Capability.NET_ADMIN),
                ).exec()
                .id
        try {
            docker.startContainerCmd(helper).exec()
            val code = docker.waitContainerCmd(helper).start().awaitStatusCode()
            check(code == 0) { "iptables ${args.joinToString(" ")} exited $code" }
        } finally {
            docker.removeContainerCmd(helper).withForce(true).exec()
        }
    }
}
