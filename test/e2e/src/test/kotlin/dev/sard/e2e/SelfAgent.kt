// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import com.github.dockerjava.api.model.AccessMode
import com.github.dockerjava.api.model.Bind
import com.github.dockerjava.api.model.Capability
import com.github.dockerjava.api.model.Volume
import org.testcontainers.DockerClientFactory
import org.testcontainers.containers.GenericContainer
import org.testcontainers.containers.startupcheck.OneShotStartupCheckStrategy
import org.testcontainers.utility.DockerImageName
import java.io.ByteArrayOutputStream
import java.util.UUID

/**
 * The agent sard-self next to the server, as deploy/docker-compose.yml runs it (F5, F6): the `-pg18` image, the
 * server's uid, a read-only root file system without capabilities, the CA and the channel read-only, its config and
 * state on volumes of their own, and an installation directory (`.env`, the compose file and a manual backup that
 * must stay out of the snapshot) mounted read-only at [INSTALL_DIR]. It enrolls itself from the channel.
 */
internal class SelfAgent(
    private val sard: SardEnvironment,
) {
    private val configVolume = sard.volume()
    private val stateVolume = sard.volume()
    private val installVolume = sard.volume()

    /** What `.env` holds; its content must come back from the snapshot byte for byte. */
    val envFile = "SARD_VERSION=e2e\nSARD_TELEGRAM_BOT_TOKEN=123:${UUID.randomUUID()}\n"

    val container: GenericContainer<*> =
        sard.track(
            "sard-self",
            GenericContainer<Nothing>(DockerImageName.parse(E2e.agentPgImage)).apply {
                withNetwork(sard.dockerNetwork)
                withCommand("--config", CONFIG, "--enroll-token-file", "$CHANNEL/enroll-token")
                withCreateContainerCmdModifier {
                    it.withUser("10001:10001").withHostName("sard-self")
                    it.hostConfig
                        ?.withReadonlyRootfs(true)
                        ?.withCapDrop(Capability.ALL)
                        ?.withSecurityOpts(listOf("no-new-privileges:true"))
                        ?.withTmpFs(mapOf("/tmp" to ""))
                        ?.withBinds(*volumes().toTypedArray())
                }
            },
        )

    init {
        sard.secret(envFile.lines()[1].substringAfter('='))
        fillInstallDirectory()
    }

    /**
     * Starts the agent and waits until it is the tenant's live built-in agent; returns its id. The server issues the
     * built-in token only after the wizard's step ca (F4a), so the wizard goes first.
     */
    fun start(): String {
        sard.ensureAdministrator()
        container.start()
        return try {
            Await.value("the built-in agent sard-self registered") { liveBuiltin() }
        } catch (e: AssertionError) {
            throw AssertionError("${e.message}\n${startLogs()}", e)
        }
    }

    /** The masked tail of the sidecar's log and the server's lines about it: why it did not register, in the test report. */
    private fun startLogs(): String {
        val agent = runCatching { container.logs }.getOrDefault("").lines().takeLast(LOG_TAIL)
        val server = sard.serverLogs().lines().filter { SELF_LINE.containsMatchIn(it) }.takeLast(LOG_TAIL)
        return sard.masked((listOf("--- sard-self:") + agent + "--- sard-server, self:" + server).joinToString("\n"))
    }

    /**
     * Adds a local repository as the onboarding tells the operator to (F4b, answer 5). The running sidecar has no
     * capabilities, so not even root in it can enter its own 0700 directories; `repo add` runs in a one-off
     * container of the service, as root with only the capabilities it needs:
     * `docker compose run --rm --no-deps -u 0 --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER --cap-add SETUID
     * --cap-add SETGID self-agent
     * repo add <name> <path> --no-restart --no-recovery-key --config /etc/sard/self/agent.yaml`, then a restart of the sidecar, which
     * reads the new fragment. Waits until Register reports the repository's id; returns the command's output.
     */
    fun addLocalRepository(
        agentId: String,
        name: String,
    ): String {
        val output = oneOff("repo", "add", name, "$STATE/$name", "--no-restart", "--no-recovery-key", "--config", CONFIG)
        container.dockerClient.restartContainerCmd(container.containerId).exec()
        Await.until("repository_id of $name in Register of sard-self") { Backups.repositoryId(sard, agentId, name) != null }
        return output
    }

    /**
     * The `recoveryKeyCommand` of the self-backup state as the operator runs it on the server's machine (V3, КС2): the
     * one-off container of `docker compose run`, with a terminal as compose gives it (the key is shown only on one),
     * and then `docker compose restart self-agent`. The test has no compose: it takes the words after `self-agent`
     * from the command itself, so a command that changes shape is not run as it was. Returns the one-off's output.
     */
    fun runRecoveryKeyCommand(command: String): String {
        val (run, restart) = command.split(" && ", limit = 2).let { it[0] to it.getOrNull(1) }
        check(run.startsWith("docker compose run --rm ") && " self-agent " in run) { "not a one-off of the sidecar: $run" }
        val output = oneOff(*run.substringAfter(" self-agent ").split(' ').toTypedArray(), terminal = true)
        if (restart != null) {
            check(restart == "docker compose restart self-agent") { "not a restart of the sidecar: $restart" }
            container.dockerClient.restartContainerCmd(container.containerId).exec()
        }
        return output
    }

    /** Where the daemon keeps the state volume: a verifier mounts a repository of it by this path (V5). */
    val statePath: String
        get() =
            DockerClientFactory
                .instance()
                .client()
                .inspectVolumeCmd(stateVolume)
                .exec()
                .mountpoint

    /**
     * A key of the repository [name] for a verifier (V3), issued in a one-off of the service as the operator runs
     * `repo key add --for verifier`; registered as a log secret.
     */
    fun verifierKey(name: String): String {
        val file = "$STATE/verifier-$name.pass"
        oneOff("repo", "key", "add", "--config", CONFIG, "--for", "verifier", "--to-file", file, "--no-restart", name)
        return container.copyFileFromContainer(file) { String(it.readAllBytes()) }.trimEnd('\n').also(sard::secret)
    }

    /**
     * Opens the repository [name] to the group of the service, as an operator shares a repository with the user of
     * another host; restic gives later files the group's access too.
     */
    fun shareWithGroup(name: String) {
        val exit = exec("10001", "chmod", "-R", "g+rX", "$STATE/$name")
        check(exit.code == 0) { "chmod exited ${exit.code}: ${exit.stderr}" }
    }

    /** The keys of the repository [name] as `repo key list --json` prints them, in the running sidecar as the agent. */
    fun keys(name: String): Json {
        val exit = exec("10001", AgentImage.AGENT_BINARY, "repo", "key", "list", "--json", "--config", CONFIG, name)
        check(exit.code == 0) { "repo key list exited ${exit.code}: ${exit.stderr}" }
        return Json.parse(exit.stdout)["keys"]
    }

    /**
     * A container of the service's image as `docker compose run --rm --no-deps -u 0 --cap-add ...` makes it, running
     * `sard-agent` with [args]; [terminal] gives it a pseudo terminal. Fails with the output unless it exits 0.
     */
    private fun oneOff(
        vararg args: String,
        terminal: Boolean = false,
    ): String {
        val output = StringBuilder()
        GenericContainer<Nothing>(DockerImageName.parse(E2e.agentPgImage)).apply {
            withCommand(*args)
            withLogConsumer { output.append(it.utf8String) }
            withCreateContainerCmdModifier {
                it.withUser("0").withTty(terminal).withStdinOpen(terminal)
                it.hostConfig
                    ?.withReadonlyRootfs(true)
                    ?.withCapDrop(Capability.ALL)
                    ?.withCapAdd(Capability.CHOWN, Capability.DAC_OVERRIDE, Capability.FOWNER, Capability.SETUID, Capability.SETGID)
                    ?.withSecurityOpts(listOf("no-new-privileges:true"))
                    ?.withTmpFs(mapOf("/tmp" to ""))
                    ?.withBinds(*volumes().toTypedArray())
            }
            withStartupCheckStrategy(OneShotStartupCheckStrategy().withTimeout(java.time.Duration.ofMinutes(2)))
        }.use { oneOff ->
            runCatching { oneOff.start() }.onFailure { throw IllegalStateException("${args.take(MAX_NAME_WORDS).joinToString(" ")} failed: $output", it) }
        }
        return output.toString()
    }

    /** The mounts of deploy/docker-compose.yml's self-agent. */
    private fun volumes(): List<Bind> =
        listOf(
            Bind(configVolume, Volume("/etc/sard/self")),
            Bind(stateVolume, Volume(STATE)),
            Bind(sard.caVolume, Volume(SardEnvironment.PKI_DIR), AccessMode.ro),
            Bind(sard.channelVolume, Volume(CHANNEL), AccessMode.ro),
            Bind(installVolume, Volume(INSTALL_DIR), AccessMode.ro),
        )

    /** The ids of the snapshots in the repository [name] that carry every one of [tags]. */
    fun snapshots(
        name: String,
        vararg tags: String,
    ): List<String> {
        val filter = tags.flatMap { listOf("--tag", it) }.toTypedArray()
        return SNAPSHOT_ID.findAll(restic(name, "snapshots", "--json", *filter)).map { it.groupValues[1] }.toList()
    }

    /** The tags of [snapshot]. */
    fun tags(
        name: String,
        snapshot: String,
    ): Set<String> {
        val json = restic(name, "snapshots", "--json", snapshot)
        val list = Regex("\"tags\":\\[([^\\]]*)]").find(json)?.groupValues?.get(1).orEmpty()
        return Regex("\"([^\"]*)\"").findAll(list).map { it.groupValues[1] }.toSet()
    }

    /** The paths [snapshot] holds, as `restic ls` lists them. */
    fun paths(
        name: String,
        snapshot: String,
    ): List<String> = restic(name, "ls", snapshot).lines().filter { it.startsWith("/") }

    /** The bytes of [file] (a path in the snapshot) of [snapshot], restored inside the agent's state volume. */
    fun restored(
        name: String,
        snapshot: String,
        file: String,
    ): ByteArray {
        val target = "$STATE/restored-$snapshot"
        restic(name, "restore", snapshot, "--target", target, "--include", file)
        return container.copyFileFromContainer("$target$file") { it.readAllBytes() }
    }

    /** A command in the running container as [user] ("10001" the agent, "0" root, as `docker compose exec -u`). */
    fun exec(
        user: String,
        vararg command: String,
    ): Exec {
        val docker = container.dockerClient
        val created =
            docker
                .execCreateCmd(container.containerId)
                .withUser(user)
                .withCmd(*command)
                .withAttachStdout(true)
                .withAttachStderr(true)
                .exec()
        val stdout = ByteArrayOutputStream()
        val stderr = ByteArrayOutputStream()
        docker
            .execStartCmd(created.id)
            .exec(
                object : com.github.dockerjava.api.async.ResultCallback.Adapter<com.github.dockerjava.api.model.Frame>() {
                    override fun onNext(frame: com.github.dockerjava.api.model.Frame) {
                        when (frame.streamType) {
                            com.github.dockerjava.api.model.StreamType.STDERR -> stderr.write(frame.payload)
                            else -> stdout.write(frame.payload)
                        }
                    }
                },
            ).awaitCompletion()
        val code = docker.inspectExecCmd(created.id).exec().exitCodeLong ?: -1
        return Exec(code.toInt(), stdout.toString(Charsets.UTF_8), stderr.toString(Charsets.UTF_8))
    }

    class Exec(
        val code: Int,
        val stdout: String,
        val stderr: String,
    )

    private fun restic(
        name: String,
        vararg args: String,
    ): String {
        val exit =
            exec(
                "10001",
                AgentImage.RESTIC_BINARY,
                *args,
                "--repo",
                "$STATE/$name",
                "--password-file",
                "/etc/sard/self/secrets/restic-$name.pass",
                "--no-cache",
            )
        check(exit.code == 0) { "restic ${args.first()} exited ${exit.code}: ${exit.stderr}" }
        return exit.stdout
    }

    private fun liveBuiltin(): String? =
        sard.database().use { connection ->
            connection
                .prepareStatement(
                    "SELECT id FROM agents WHERE builtin AND revoked_at IS NULL AND last_register_at IS NOT NULL",
                ).use { q -> q.executeQuery().use { if (it.next()) it.getString(1) else null } }
        }

    /** `.env` group 10001 and 0640, as docs/operator/02-install.md says; the compose file; a manual backup. */
    private fun fillInstallDirectory() {
        val script =
            "printf '%s' \"\$ENV_FILE\" > /i/.env && chgrp 10001 /i/.env && chmod 640 /i/.env" +
                " && echo 'services: {}' > /i/docker-compose.yml && echo junk > /i/sard-db-2026-10-01.dump"
        GenericContainer<Nothing>(DockerImageName.parse(E2e.agentPgImage)).apply {
            withEnv("ENV_FILE", envFile)
            withCreateContainerCmdModifier {
                it.withUser("0").withEntrypoint("sh")
                it.hostConfig?.withBinds(Bind(installVolume, Volume("/i")))
            }
            withCommand("-c", script)
            withStartupCheckStrategy(OneShotStartupCheckStrategy())
        }.use { it.start() }
    }

    companion object {
        const val CONFIG = "/etc/sard/self/agent.yaml"
        const val STATE = "/var/lib/sard-self"
        const val CHANNEL = "/run/sard-self"

        /** Where the server's settings expect the installation directory (`sard.self-backup.install-dir`). */
        const val INSTALL_DIR = "/etc/sard/install"

        private val SNAPSHOT_ID = Regex("\"id\":\"([0-9a-f]{64})\"")
        private const val LOG_TAIL = 40
        private const val MAX_NAME_WORDS = 3
        private val SELF_LINE = Regex("(?i)self|builtin|enroll")
    }
}
