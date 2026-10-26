// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import com.github.dockerjava.api.model.AccessMode
import com.github.dockerjava.api.model.Bind
import com.github.dockerjava.api.model.Volume
import org.testcontainers.DockerClientFactory
import org.testcontainers.containers.GenericContainer
import java.nio.file.Files
import java.nio.file.Path

/**
 * A verifier whose checks start sandboxes (V5, phase 3): the image with the Docker CLI ([E2e.verifierImage]), the
 * stand's Docker socket with its group, as a verifier host's service user in the group docker, and a work volume
 * mounted noexec (tmpfs) at the path the daemon knows it by. The daemon mounts the directory of a job into the
 * sandbox by that path, as on a host where the agent and the daemon share one file system. A holder container keeps
 * the tmpfs mounted while the verifier restarts.
 */
internal class VerifierStand(
    private val sard: SardEnvironment,
    private val verifications: Verifications,
) {
    /** A repository the verifier reads: [bind] mounted read-only at /srv/[name], opened with [key] in [keyFile]. */
    class Shared(
        val name: String,
        val bind: (String) -> Bind,
        val keyFile: String,
        val key: String,
        /** A group the verifier needs to read it, as an operator shares a directory with another user. */
        val group: String? = null,
    )

    private val work = sard.noexecVolume()
    private val holder = verifications.holder(work)

    /** The work volume as the daemon, and the verifier, see it. */
    val workDir: String = DockerClientFactory.instance().client().inspectVolumeCmd(work).exec().mountpoint

    private lateinit var shared: List<Shared>
    lateinit var agent: EnrolledAgent
        private set
    private var container: GenericContainer<*>? = null

    /** Enrolls the verifier with [repositories]; [start] runs it. */
    fun enroll(repositories: List<Shared>) {
        shared = repositories
        val socketGroup = Files.getAttribute(Path.of(DOCKER_SOCKET), "unix:gid").toString()
        val host =
            AgentHost(
                sard, "pg-verifier", config(""), image = E2e.verifierImage,
                mounts =
                    repositories.map { it.bind("/srv/${it.name}") } +
                        listOf(Bind(workDir, Volume(workDir)), Bind(DOCKER_SOCKET, Volume(DOCKER_SOCKET))),
                groups = listOf(socketGroup) + repositories.mapNotNull { it.group },
            )
        agent = EnrolledAgent(AgentEnroller.enroll(host, EnrollmentTokens.create(sard, role = "verifier")), host)
    }

    /** (Re)starts the verifier with [verify] added to its `verify` section, until it reports every repository. */
    fun start(verify: String = "") {
        container?.let { it.dockerClient.stopContainerCmd(it.containerId).exec() }
        agent.host.local = config(verify)
        container = sard.track("verifier-${++starts}", AgentContainer.of(agent, shared.associate { it.keyFile to it.key })).apply { start() }
        Await.until("the verifier online with its repositories") {
            shared.all { Backups.repositoryId(sard, agent.agentId, it.name) != null } && sandboxReported()
        }
    }

    private var starts = 0

    /** The server has the verifier's sandbox runtime from Register or a heartbeat. */
    private fun sandboxReported(): Boolean =
        verifications.query("SELECT verify_sandbox FROM agents WHERE id = ?::uuid", agent.agentId) { it.getString(1) } == "docker"

    private fun config(verify: String): String =
        "repositories:\n" +
            sharedSoFar().joinToString("") { "  - {name: ${it.name}, url: /srv/${it.name}, password_file: ${it.keyFile}}\n" } +
            "verify:\n  work_dir: $workDir\n  check_percent: 100\n" + verify

    private fun sharedSoFar(): List<Shared> = if (::shared.isInitialized) shared else emptyList()

    /** The names on the work volume, as its holder sees them. */
    fun workVolume(): List<String> = holder.execInContainer("ls", "-A", "/verify").stdout.lines().filter { it.isNotBlank() }

    companion object {
        private const val DOCKER_SOCKET = "/var/run/docker.sock"

        /** A named [volume] mounted read-only. */
        fun readOnly(volume: String): (String) -> Bind = { path -> Bind(volume, Volume(path), AccessMode.ro) }
    }
}
