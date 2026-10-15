// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.testcontainers.containers.BindMode
import org.testcontainers.containers.GenericContainer
import org.testcontainers.containers.wait.strategy.Wait
import org.testcontainers.images.builder.Transferable
import org.testcontainers.utility.DockerImageName
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.TimeUnit

/**
 * One "VM" of the recovery drill (F7, Р39): a privileged container of `sard-recovery-vm:e2e` (test/e2e/recovery-vm)
 * with a Docker daemon of its own, so its volumes, ports and containers are its own. It sits on the drill's network,
 * which leads nowhere else, under [aliases]. [offline] is the directory with the offline archive of the release's images
 * (docs/operator/11-offline.md), mounted as `~/sard-offline` read-only. Commands run in it as an operator's shell does:
 * `bash -c` as root with the home directory `/root`, the typed input on the standard input.
 */
internal class RecoveryVm(
    private val sard: SardEnvironment,
    val name: String,
    offline: Path,
    aliases: List<String> = listOf(name),
) {
    class Exit(
        val code: Int,
        val stdout: String,
        val stderr: String,
    ) {
        override fun toString() = "Exit(code=$code)"
    }

    val container: GenericContainer<*> =
        sard.track(
            name,
            GenericContainer<Nothing>(DockerImageName.parse(E2e.recoveryVmImage)).apply {
                withNetwork(sard.dockerNetwork)
                withNetworkAliases(*aliases.toTypedArray())
                withFileSystemBind(offline.toString(), "/root/sard-offline", BindMode.READ_ONLY)
                withCreateContainerCmdModifier { it.hostConfig?.withPrivileged(true) }
                waitingFor(Wait.forLogMessage(".*API listen on.*", 1).withStartupTimeout(STARTUP))
            },
        )

    /** Starts the machine and waits until its Docker daemon answers. */
    fun start(): RecoveryVm {
        container.start()
        Await.until("the Docker daemon of $name", STARTUP) { run("docker info > /dev/null 2>&1", timeout = Duration.ofSeconds(30)).code == 0 }
        return this
    }

    /**
     * Runs [script] with `bash -c`, [stdin] lines on the standard input. The script is the text of a block of the page
     * (or a command of the drill's own set-up), so it holds no secret; secrets come only on [stdin].
     */
    fun run(
        script: String,
        stdin: List<String> = emptyList(),
        timeout: Duration = COMMAND_TIMEOUT,
    ): Exit {
        val process =
            ProcessBuilder("docker", "exec", "-i", container.containerId, "bash", "-c", script)
                .redirectErrorStream(false)
                .start()
        val out = StringBuilder()
        val err = StringBuilder()
        val readers =
            listOf(
                Thread { process.inputStream.bufferedReader().forEachLine { out.append(it).append('\n') } },
                Thread { process.errorStream.bufferedReader().forEachLine { err.append(it).append('\n') } },
            ).onEach { it.start() }
        process.outputStream.use { stream -> stdin.forEach { stream.write((it + "\n").toByteArray()) } }
        if (!process.waitFor(timeout.toMillis(), TimeUnit.MILLISECONDS)) {
            process.destroyForcibly()
            readers.forEach { it.join(JOIN_MILLIS) }
            throw AssertionError("the command did not end within $timeout in $name: ${script.lines().first()}\n$out\n$err")
        }
        readers.forEach { it.join(JOIN_MILLIS) }
        return Exit(process.exitValue(), out.toString(), err.toString())
    }

    /** [script] must end with code 0; returns its standard output. */
    fun succeed(
        script: String,
        stdin: List<String> = emptyList(),
        timeout: Duration = COMMAND_TIMEOUT,
    ): String {
        val exit = run(script, stdin, timeout)
        check(exit.code == 0) { "in $name exited ${exit.code}: ${script.lines().first()}\n${exit.stdout}\n${exit.stderr}" }
        return exit.stdout
    }

    /** A file in the machine, with the mode given. */
    fun put(
        path: String,
        content: ByteArray,
        mode: Int = TarFiles.READABLE,
    ) {
        container.copyFileToContainer(Transferable.of(content, mode), path)
    }

    private var removedId: String? = null

    /** The machine and its disks are gone: the container is removed with its volumes. */
    fun destroy() {
        removedId = container.containerId
        container.stop()
    }

    /** True once [destroy] has run and Docker no longer knows the container. */
    fun isGone(): Boolean {
        val id = removedId ?: return false
        return ProcessBuilder("docker", "inspect", id).redirectErrorStream(true).start().waitFor() != 0
    }

    /** The IP of the machine on the drill's network. */
    fun address(): String =
        checkNotNull(
            container.currentContainerInfo.networkSettings.networks.values
                .first()
                .ipAddress,
        )

    private companion object {
        val STARTUP: Duration = Duration.ofMinutes(2)
        val COMMAND_TIMEOUT: Duration = Duration.ofMinutes(10)
        const val JOIN_MILLIS = 5_000L
    }
}
