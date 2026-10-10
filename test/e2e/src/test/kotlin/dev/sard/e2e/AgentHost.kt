// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import com.github.dockerjava.api.DockerClient
import com.github.dockerjava.api.model.Bind
import com.github.dockerjava.api.model.Volume
import org.apache.commons.compress.archivers.tar.TarArchiveInputStream
import org.testcontainers.containers.GenericContainer
import org.testcontainers.containers.output.OutputFrame
import org.testcontainers.containers.startupcheck.StartupCheckStrategy
import org.testcontainers.containers.wait.strategy.Wait
import org.testcontainers.images.builder.Transferable
import org.testcontainers.utility.DockerImageName
import java.time.Duration

/**
 * One agent host: the disk of a machine with the agent package installed, and every command an
 * operator runs on it. The disk is two named volumes mounted where the package keeps its state
 * and the restic cache (`/var/lib/sard-agent`, `/var/cache/sard/restic`; Docker fills a fresh
 * volume with the image's directory, owner 65532, mode 0700). Each command is a container of the
 * agent image on those volumes with the program as its entry point, as the service user:
 * `sard-agent enroll`, `sard-agent repo init`, `restic restore`, `ssh-keygen`, and the agent itself
 * (ADR 0020; the image is a host with openssh-client since ADR 0047).
 *
 * The config is the same for every command: `server.address`, the tls files on the state volume
 * (where `sard-agent enroll` writes them), plus [local] (repositories, secrets). Its copy in each
 * container is `/etc/sard/agent.yaml`, as the service unit passes it.
 */
internal class AgentHost(
    // Not "env": inside a container's apply {} that name is the container's own getEnv().
    private val sardEnv: SardEnvironment,
    val hostname: String = "e2e-agent",
    val local: String = "",
    /** The agent image every command of this host runs: [E2e.agentImage] unless T3 needs the stand's. */
    private val image: String = E2e.agentImage,
    /**
     * `/etc/sard` on a volume of the host, as on a real one (Н24, ADR 0047): `agent.d` and `secrets`
     * written by `sudo sard-agent repo add` stay for the next command and for the agent. Without it
     * (the default) the config is copied into every container and nothing else of `/etc/sard` stays.
     */
    persistentEtc: Boolean = false,
) {
    /** The volume mounted at [STATE_DIR]: state, TLS files, repository, the executor's journal. */
    val state = sardEnv.volume()
    private val cache = sardEnv.volume()
    private val etc = if (persistentEtc) sardEnv.volume() else null
    private var runs = 0

    /** How a command ended. Its output is in the environment's failure logs, masked. */
    class Exit(
        val code: Int,
        val stdout: String,
        val stderr: String,
    ) {
        override fun toString() = "Exit(code=$code)"
    }

    /**
     * Runs [command] (the program and its arguments) to completion. [files] are copied in first
     * (a path in the container to its content and owner); [env] is its environment. [inspect] sees
     * the stopped container before it is removed (`docker cp` works on it).
     */
    fun run(
        vararg command: String,
        files: Map<String, Transferable> = emptyMap(),
        env: Map<String, String> = emptyMap(),
        timeout: Duration = RUN_TIMEOUT,
        asRoot: Boolean = false,
        inspect: (GenericContainer<*>) -> Unit = {},
    ): Exit {
        val name = "$hostname-run-${++runs}"
        container().use { container ->
            sardEnv.track(name, container)
            files.forEach { (path, content) -> container.withCopyToContainer(content, path) }
            container.withEnv(env)
            container.withCreateContainerCmdModifier { create ->
                create.withEntrypoint(*command)
                // `sudo`: the command runs as root, which the image has no sudo for (ADR 0050).
                if (asRoot) create.withUser("0")
            }
            container.withStartupCheckStrategy(Exited.withTimeout(timeout))
            container.start()
            inspect(container)
            val code = container.currentContainerInfo.state.exitCodeLong ?: error("$name has no exit code")
            return Exit(
                code.toInt(),
                container.getLogs(OutputFrame.OutputType.STDOUT),
                container.getLogs(OutputFrame.OutputType.STDERR),
            )
        }
    }

    /**
     * Starts [command] on this host and returns while it runs, for a test to interrupt it
     * ([Interruptions.kill]): an operator's command that never finished. Tracked by the environment.
     */
    fun launch(
        vararg command: String,
        env: Map<String, String> = emptyMap(),
    ): GenericContainer<*> {
        val name = "$hostname-run-${++runs}"
        return sardEnv.track(name, container()).apply {
            withEnv(env)
            withCreateContainerCmdModifier { it.withEntrypoint(*command) }
            start()
        }
    }

    /**
     * `sard-agent enroll --server <endpoint> --token-file <file> [flags]`: the operator's command
     * from the console, with the token in a file so it never shows up in the container's command.
     */
    fun enroll(
        token: String,
        vararg flags: String,
        timeout: Duration = RUN_TIMEOUT,
    ): Exit =
        run(
            AgentImage.AGENT_BINARY, "enroll", "--config", CONFIG,
            "--server", SardEnvironment.AGENT_ENDPOINT, "--token-file", TOKEN_FILE, *flags,
            files = mapOf(TOKEN_FILE to TarFiles.ownedByAgent(token + "\n")),
            timeout = timeout,
        )

    /** `sard-agent repo init <name> [flags]` with the host's config (A5b). */
    fun repoInit(
        name: String,
        vararg flags: String,
    ): Exit = run(AgentImage.AGENT_BINARY, "repo", "init", "--config", CONFIG, *flags, name)

    /**
     * `sudo sard-agent repo add <name> <address> [flags]` (A8b): as root, on the host's `/etc/sard`
     * and state volumes. [secretKey] reaches `--secret-key-stdin` from a file inside the container
     * (a container has no stdin to feed), never the command line; [files] are copied in first.
     */
    fun repoAdd(
        name: String,
        address: String,
        vararg flags: String,
        secretKey: String? = null,
        files: Map<String, Transferable> = emptyMap(),
        timeout: Duration = RUN_TIMEOUT,
    ): Exit {
        check(etc != null) { "repo add needs a host with persistentEtc" }
        secretKey?.let(sardEnv::secret)
        val stdin = if (secretKey != null) " --secret-key-stdin < $SECRET_KEY_FILE" else ""
        val line = listOf(AgentImage.AGENT_BINARY, "repo", "add", "--config", CONFIG, name, address, *flags).joinToString(" ") { "'$it'" }
        val keyFile = secretKey?.let { mapOf(SECRET_KEY_FILE to Transferable.of(it.toByteArray(), TarFiles.OWNER_ONLY)) }.orEmpty()
        return run("/bin/sh", "-c", "exec $line$stdin", files = files + keyFile, timeout = timeout, asRoot = true)
    }

    /** `sudo sard-agent repo password <name> --reveal`: the password, with its line break dropped; registered as a log secret. */
    fun revealPassword(name: String): String {
        val exit = run(AgentImage.AGENT_BINARY, "repo", "password", "--config", CONFIG, "--reveal", name, asRoot = true)
        check(exit.code == 0) { "repo password exited ${exit.code}: ${exit.stderr}" }
        return exit.stdout.trimEnd('\n').also(sardEnv::secret)
    }

    /** Owner and mode of [path] on this host, as `uid:gid mode` (`stat`). */
    fun stat(path: String): String {
        val exit = run("/usr/bin/stat", "-c", "%u:%g %a", path)
        check(exit.code == 0) { "stat $path exited ${exit.code}: ${exit.stderr}" }
        return exit.stdout.trim()
    }

    /** Whether [path] exists on this host. */
    fun exists(path: String): Boolean = run("/usr/bin/test", "-e", path).code == 0

    /** Puts [files] at [destination] on this host's disk (a path on one of its volumes). */
    fun put(
        destination: String,
        files: TarFiles,
    ) {
        val exit = run(AgentImage.RESTIC_BINARY, "version", files = mapOf(destination to files))
        check(exit.code == 0) { "copying to $destination: restic version exited ${exit.code}" }
    }

    /**
     * `restic restore <snapshotId>` from the repository at [repository] into a fresh directory on
     * this host, then the restored copy of [path] read back: relative path to bytes, null for a
     * directory, [path] itself left out. The password file goes to restic by path; the test never
     * reads it. [env] carries the backend's credentials (an S3 key), as the repository's env_file does.
     */
    fun restore(
        repository: String,
        passwordFile: String,
        snapshotId: String,
        path: String,
        env: Map<String, String> = emptyMap(),
    ): Map<String, ByteArray?> {
        val target = "$STATE_DIR/restored-$snapshotId"
        var tree: Map<String, ByteArray?> = emptyMap()
        val exit =
            run(
                AgentImage.RESTIC_BINARY, "restore", snapshotId, "--repo", repository, "--password-file", passwordFile,
                "--no-cache", "--target", target,
                env = env,
            ) { c -> tree = readTree(c, target + path) }
        check(exit.code == 0) { "restic restore exited ${exit.code}: ${exit.stderr}" }
        return tree
    }

    /** The bytes of [path] on this host (read through a container of the image: `docker cp`). */
    fun read(path: String): ByteArray {
        var bytes: ByteArray? = null
        val exit = run(AgentImage.RESTIC_BINARY, "version") { c -> bytes = c.copyFileFromContainer(path) { it.readAllBytes() } }
        check(exit.code == 0) { "reading $path: restic version exited ${exit.code}" }
        return checkNotNull(bytes)
    }

    /**
     * The service user's SSH client setup for an SFTP repository, as an operator does it: `~/.ssh`
     * (the user's home, [STATE_DIR]: ssh reads passwd, not the HOME the agent gives restic) with
     * [knownHosts], the server keys the operator verified, then `ssh-keygen` as the service user.
     * Returns the public key to authorize on the server.
     */
    fun sshClient(knownHosts: String): String {
        put(SSH_DIR, TarFiles(listOf(TarFiles.Entry("", null, TarFiles.OWNER_DIR), TarFiles.Entry("known_hosts", knownHosts.toByteArray(), TarFiles.READABLE))))
        val keygen = run("/usr/bin/ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", hostname, "-f", "$SSH_DIR/id_ed25519")
        check(keygen.code == 0) { "ssh-keygen exited ${keygen.code}: ${keygen.stderr}" }
        return String(read("$SSH_DIR/id_ed25519.pub"))
    }

    /** What `sard-agent enroll` left on this host; the key is registered as a log secret. */
    fun credentials(agentId: String): AgentCredentials {
        val key = String(read(KEY_FILE)).also(sardEnv::secret)
        return AgentCredentials(agentId, key, String(read(CERT_FILE)), String(read(CA_FILE)))
    }

    /**
     * The agent service of this host on the environment's network, started by the caller (and
     * tracked by it, see [SardEnvironment.track]). [ownedFiles] (path to content) are written 0600
     * and owned by the agent, as the agent requires of secret files (A1).
     */
    fun agent(ownedFiles: Map<String, String> = emptyMap()): GenericContainer<*> =
        container().apply {
            ownedFiles.forEach { (path, content) -> withCopyToContainer(TarFiles.ownedByAgent(content), path) }
            waitingFor(Wait.forLogMessage(".*connecting to ${SardEnvironment.AGENT_ENDPOINT}.*", 1))
        }

    /** The tree at [path] in [container] (`docker cp` as a tar): relative path to bytes, null for a directory. */
    private fun readTree(
        container: GenericContainer<*>,
        path: String,
    ): Map<String, ByteArray?> {
        val tree = linkedMapOf<String, ByteArray?>()
        container.dockerClient.copyArchiveFromContainerCmd(container.containerId, path).exec().use { stream ->
            val tar = TarArchiveInputStream(stream, Charsets.UTF_8.name())
            generateSequence { tar.nextEntry }.forEach { entry ->
                // Entries are named from the copied directory's own name: "data/nested/...".
                val relative = entry.name.substringAfter('/', "").trimEnd('/')
                if (relative.isNotEmpty()) tree[relative] = if (entry.isDirectory) null else tar.readAllBytes()
            }
        }
        return tree
    }

    private fun container(): GenericContainer<*> =
        GenericContainer<Nothing>(DockerImageName.parse(image)).apply {
            withNetwork(sardEnv.dockerNetwork)
            withNetworkAliases(hostname)
            withCopyToContainer(Transferable.of(config(), TarFiles.READABLE), CONFIG)
            withCreateContainerCmdModifier { create ->
                create.withHostName(hostname)
                create.hostConfig?.withBinds(*binds().toTypedArray())
            }
        }

    private fun binds(): List<Bind> =
        listOfNotNull(Bind(state, Volume(STATE_DIR)), Bind(cache, Volume(CACHE_DIR)), etc?.let { Bind(it, Volume(ETC_DIR)) })

    private fun config() =
        """
        server:
          address: ${SardEnvironment.AGENT_ENDPOINT}
        tls:
          ca_file: $CA_FILE
          cert_file: $CERT_FILE
          key_file: $KEY_FILE
        """.trimIndent() + "\n" + local

    /** Done once the container has stopped, whatever its exit code: the caller judges the code. */
    private object Exited : StartupCheckStrategy() {
        override fun checkStartupState(
            dockerClient: DockerClient,
            containerId: String,
        ): StartupStatus {
            val state = dockerClient.inspectContainerCmd(containerId).exec().state
            return if (state.running == false && state.finishedAt != null) StartupStatus.SUCCESSFUL else StartupStatus.NOT_YET_KNOWN
        }
    }

    companion object {
        const val ETC_DIR = "/etc/sard"
        const val CONFIG = "$ETC_DIR/agent.yaml"

        /** What `sudo sard-agent repo add` makes under [ETC_DIR] (A8b, Р30). */
        const val SECRETS_DIR = "$ETC_DIR/secrets"
        const val FRAGMENT_DIR = "$ETC_DIR/agent.d"
        private const val SECRET_KEY_FILE = "/run/secret-key"
        const val STATE_DIR = "/var/lib/sard-agent"
        const val CACHE_DIR = "/var/cache/sard/restic"
        const val CA_FILE = "$STATE_DIR/ca.pem"
        const val CERT_FILE = "$STATE_DIR/agent.pem"
        const val KEY_FILE = "$STATE_DIR/agent.key"

        /** The service user's `~/.ssh`: its home is [STATE_DIR] (deploy/agent/postinstall.sh). */
        const val SSH_DIR = "$STATE_DIR/.ssh"

        /** The executor's state dir: the agent's default (agent/cmd/sard-agent/main.go). */
        const val EXECUTOR_DIR = "$STATE_DIR/executor"
        private const val TOKEN_FILE = "/etc/sard/token"
        private val RUN_TIMEOUT = Duration.ofMinutes(2)
    }
}
