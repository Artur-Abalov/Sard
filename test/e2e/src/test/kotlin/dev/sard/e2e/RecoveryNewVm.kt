// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.nio.file.Path
import java.nio.file.Files
import java.time.Duration
import java.time.Instant
import java.util.concurrent.TimeUnit

/** One block of the page as it was run on the new machine. */
internal class BlockRun(
    val id: String,
    val text: String,
    val code: Int,
    val stdout: String,
    val stderr: String,
)

/**
 * The new machine of the drill (Р39, Р41): empty, with no way out of the drill's network; only the offline archive of the
 * images, the storage address and keys, the repository password, the fields of the card and the administrator's password
 * reach it, and only as the page tells the operator to enter them. The blocks of the page marked for the drill are run
 * as written, in the order of the page, with the values in angle brackets taken from the drill's table and the typed
 * secrets on the standard input; [advance] runs the blocks up to the one named. The two things the page leaves to the
 * operator are done by the drill: the DNS name moves to the machine (the network alias), and the agent that got
 * CERT_UNKNOWN is enrolled again on its own host with a shell of that host.
 */
internal class RecoveryNewVm(
    private val sard: SardEnvironment,
    private val old: OldInstallation,
    offline: Path,
    page: String,
) {
    val vm = RecoveryVm(sard, "vm-new", offline)
    val api = VmApi(vm)
    val runs = linkedMapOf<String, BlockRun>()
    val blocks: List<DrillBlock> = DrillPage.blocks(page)

    /** What the machine is given by the drill's table: the card and the storage address; the names are the page's. */
    val values: Map<String, String> =
        mapOf(
            "repositoryName" to old.card.repositoryName,
            "location" to old.card.location,
            "repositoryId" to old.card.repositoryId,
            "caFingerprint" to old.card.caFingerprint,
            "sardVersion" to old.card.sardVersion,
            "адрес-restic" to old.card.location,
            "образ-агента" to "sard-agent:${old.version}-pg18",
            "имя-сервера" to RecoveryOldVm.SERVER_NAME,
        )

    /** What is typed on the machine, block by block (read -rs). */
    val typed: Map<String, List<String>> =
        mapOf(
            "prepare" to listOf(old.resticPassword, old.s3Key.id, old.s3Key.secret, GarageS3.REGION),
            "login" to listOf(old.adminPassword),
        )

    private val prepared: List<PreparedBlock> = DrillPage.prepare(blocks, values)
    private var started: Instant? = null
    private var signedIn = false

    /** Seconds from the start of the first block to the server's health, to the agents online, to the self-backup runs. */
    var secondsToServer = 0L
        private set
    var secondsToAgents = 0L
        private set
    var secondsToSelfBackup = 0L
        private set

    /** The executed texts of the blocks, after the substitutions: what could show a secret. */
    val commands: List<String> get() = runs.values.map { it.text }

    fun start() {
        vm.start()
    }

    /** Runs the blocks of the page, in order, up to and including [id]; the ones already run are not run again. */
    fun advance(id: String) {
        check(prepared.any { it.id == id }) { "the page has no block $id for the drill" }
        for (block in prepared) {
            val done = runs[block.id]
            if (done == null) execute(block) else check(done.code == 0) { "block ${block.id} failed earlier with code ${done.code}" }
            if (block.id == id) return
        }
    }

    private fun execute(block: PreparedBlock) {
        if (started == null) started = Instant.now()
        beforeBlock(block.id)
        val exit = runBlock(block)
        val run = BlockRun(block.id, block.text, exit.code, exit.stdout, exit.stderr)
        runs[block.id] = run
        keepOutput(run)
        check(exit.code == 0) { "block ${block.id} exited with code ${exit.code}\n${sard.masked(exit.stdout)}\n${sard.masked(exit.stderr)}" }
        afterBlock(block.id)
    }

    /** The output of the block, masked, next to the result of the drill: what a run of CI shows when a block fails. */
    private fun keepOutput(run: BlockRun) {
        val dir = Files.createDirectories(E2e.recoveryDrillDir.resolve("blocks"))
        val text = "exit ${run.code}\n--- stdout\n${run.stdout}\n--- stderr\n${run.stderr}"
        Files.writeString(dir.resolve("${run.id}.log"), sard.masked(text))
    }

    private fun runBlock(block: PreparedBlock): RecoveryVm.Exit =
        if (block.id == REENROLL) {
            reenroll(block)
        } else {
            vm.run(block.text, stdin = typed[block.id].orEmpty(), timeout = Duration.ofMinutes(BLOCK_MINUTES))
        }

    private fun beforeBlock(id: String) {
        if (id == "agents-online") switchName()
    }

    private fun afterBlock(id: String) {
        when (id) {
            "server" -> secondsToServer = elapsed()
            "backup-now" -> secondsToSelfBackup = elapsed()
        }
    }

    private fun elapsed() = Duration.between(checkNotNull(started), Instant.now()).seconds

    /** Signs the drill's own session in, once: the page's block `login` keeps its cookie in the operator's directory. */
    fun signIn() {
        if (signedIn) return
        api.login(old.adminPassword)
        signedIn = true
    }

    /** DNS: the name of the server moves to the new machine; the agents find it on their next attempt (Р39). */
    private fun switchName() {
        val network = sard.dockerNetwork.id
        val id = vm.container.containerId
        docker("network", "disconnect", network, id)
        docker("network", "connect", "--alias", RecoveryOldVm.SERVER_NAME, "--alias", vm.name, network, id)
        signIn()
        Await.until("agents A1 and A2 online on the new machine", AGENTS_ONLINE) {
            val online = api.items("/api/v1/agents").filter { it["status"].text == "online" }.map { it["id"].text }
            listOf("A1", "A2").all { old.agents.getValue(it).id in online }
        }
        secondsToAgents = elapsed()
    }

    /**
     * The page's block for the agent that got CERT_UNKNOWN, run on that agent's own host: a shell of the host with the
     * state of A3, `sudo` that runs the command, `systemctl` and `journalctl` that only say what they were asked. The
     * token is issued by the new server, as the operator does in the console, and typed to the block.
     */
    private fun reenroll(block: PreparedBlock): RecoveryVm.Exit {
        signIn()
        val token = api.created("/api/v1/enrollment-tokens", """{"ttlSeconds":3600,"label":"A3 again"}""").json["token"].text!!
        sard.secret(token)
        val host = old.agents.getValue("A3").host
        val preamble =
            listOf(
                "sudo() { \"\$@\"; }",
                "systemctl() { echo \"systemctl \$*\"; }",
                "journalctl() { echo \"journalctl \$*\"; }",
                "mkdir -p /etc/sard",
                "cat > /etc/sard/agent.yaml <<'YAML'",
                host.configText().trimEnd(),
                "YAML",
            ).joinToString("\n")
        return hostShell(host, preamble + "\n" + block.text, listOf(token))
    }

    private fun hostShell(
        host: AgentHost,
        script: String,
        stdin: List<String>,
    ): RecoveryVm.Exit {
        val command =
            listOf(
                "docker", "run", "--rm", "-i", "--user", "0", "--network", sard.dockerNetwork.id,
                "-v", "${host.state}:${AgentHost.STATE_DIR}", "-v", "${host.cache}:${AgentHost.CACHE_DIR}",
                "--entrypoint", "bash", E2e.agentImage, "-c", script,
            )
        val process = ProcessBuilder(command).start()
        val out = StringBuilder()
        val err = StringBuilder()
        val readers =
            listOf(
                Thread { process.inputStream.bufferedReader().forEachLine { out.append(it).append('\n') } },
                Thread { process.errorStream.bufferedReader().forEachLine { err.append(it).append('\n') } },
            ).onEach { it.start() }
        process.outputStream.use { stream -> stdin.forEach { stream.write((it + "\n").toByteArray()) } }
        check(process.waitFor(HOST_SHELL_MINUTES, TimeUnit.MINUTES)) { "the host shell of A3 did not end" }
        readers.forEach { it.join() }
        return RecoveryVm.Exit(process.exitValue(), out.toString(), err.toString())
    }

    private fun docker(vararg args: String) {
        val process = ProcessBuilder(listOf("docker") + args).redirectErrorStream(true).start()
        val output = process.inputStream.bufferedReader().readText()
        check(process.waitFor() == 0) { "docker ${args.first()} ${args.getOrNull(1)} failed: $output" }
    }

    /** Where the secrets of the drill show up on the new machine: in a block run, in a container's arguments, in a log. */
    fun leaks(secrets: Collection<String>): List<String> {
        val found = mutableListOf<String>()
        fun scan(
            what: String,
            text: String,
        ) = secrets.filter { it.isNotEmpty() && it in text }.forEach { found += "$what shows a secret of the drill" }
        commands.forEach { scan("a block run", it) }
        scan("the arguments of the containers", vm.succeed("docker ps -aq | xargs -r docker inspect --format '{{.Path}} {{join .Args \" \"}}'"))
        scan("the logs of the containers", vm.succeed("for c in \$(docker ps -aq); do docker logs \"\$c\" 2>&1; done"))
        scan("the log of the machine", vm.container.logs)
        return found
    }

    /** `docker ps -a` and the logs of every container of the machine, for the report of a failed drill. */
    fun nestedLogs(): String =
        runCatching {
            vm.run("docker ps -a; for c in \$(docker ps -aq); do echo \"=== \$c\"; docker logs \"\$c\" 2>&1 | tail -n 200; done").stdout
        }.getOrDefault("")

    private companion object {
        const val REENROLL = "reenroll"
        const val BLOCK_MINUTES = 12L
        const val HOST_SHELL_MINUTES = 3L
        val AGENTS_ONLINE: Duration = Duration.ofMinutes(4)
    }
}
