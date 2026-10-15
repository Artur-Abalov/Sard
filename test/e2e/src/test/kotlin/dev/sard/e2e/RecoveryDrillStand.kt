// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.extension.AfterAllCallback
import org.junit.jupiter.api.extension.BeforeAllCallback
import org.junit.jupiter.api.extension.ExtensionContext
import org.junit.jupiter.api.extension.TestWatcher
import java.nio.file.Files
import java.nio.file.Path

/**
 * The stand of [ServerRecoveryDrillTest] (Р39–Р41): an isolated network with the storage (Garage of the F2 stand) outside
 * both machines, the offline archive of the images of the build, the old machine built and destroyed in `beforeAll`
 * (Р40) and the new machine, empty, left for the page. Nothing is shared with other classes. When the stand or a test
 * fails, the logs of every container, with secrets masked, and the logs inside the new machine go to the e2e-logs.
 */
class RecoveryDrillStand :
    BeforeAllCallback,
    AfterAllCallback,
    TestWatcher {
    val sard = SardEnvironment(internalNetwork = true)
    internal lateinit var old: OldInstallation
        private set
    internal lateinit var garage: GarageS3
        private set
    internal lateinit var new: RecoveryNewVm
        private set

    private var oldVm: RecoveryOldVm? = null
    private var testClass = ""

    /** The directory of the archive, `~/sard-offline` of the machines. */
    private val offline: Path get() = E2e.recoveryDrillDir.resolve("offline")

    override fun beforeAll(context: ExtensionContext) {
        testClass = context.requiredTestClass.simpleName
        try {
            Files.createDirectories(E2e.recoveryDrillDir)
            listOf("result.json", "result.md").forEach { Files.deleteIfExists(E2e.recoveryDrillDir.resolve(it)) }
            archive()
            garage = GarageS3(sard).start()
            garage.bucket(RecoveryOldVm.BUCKET)
            val endpoint = "http://${garageAddress()}:${GarageS3.S3_PORT}"
            oldVm = RecoveryOldVm(sard, garage, endpoint, offline, E2e.version)
            old = oldVm!!.build()
            oldVm!!.destroy()
            new = RecoveryNewVm(sard, old, offline, Files.readString(E2e.recoveryPage))
            new.start()
        } catch (e: Throwable) {
            writeLogs("start")
            throw e
        }
    }

    override fun afterAll(context: ExtensionContext) = sard.stop()

    /** Р40: the old machine is destroyed, its container and all its volumes. */
    fun oldMachineIsGone(): Boolean = oldVm?.isGone() == true

    override fun testFailed(
        context: ExtensionContext,
        cause: Throwable?,
    ) = writeLogs(context.requiredTestMethod.name)

    /** The address of the storage on the drill's network: the machines have no DNS of the host. */
    internal fun garageAddress(): String =
        checkNotNull(
            garage.container.currentContainerInfo.networkSettings.networks.values
                .first()
                .ipAddress,
        )

    private fun writeLogs(step: String) {
        val dir = sard.writeLogs(testClass, step)
        if (::new.isInitialized) Files.writeString(dir.resolve("vm-new-containers.log"), sard.masked(new.nestedLogs()))
    }

    /**
     * The release's offline archive (docs/operator/11-offline.md) of the images of this build: the server, the agent
     * with pg_dump 18 and PostgreSQL, tagged as the compose file wants them for [E2e.version].
     */
    private fun archive() {
        val version = E2e.version
        host("docker", "tag", E2e.serverImage, "sard-server:$version")
        host("docker", "tag", E2e.agentPgImage, "sard-agent:$version-pg18")
        val arch = if (System.getProperty("os.arch") in setOf("aarch64", "arm64")) "arm64" else "amd64"
        Files.createDirectories(offline)
        val file = offline.resolve("sard-$version-images-linux-$arch.tar.gz")
        host("sh", "-c", "docker save sard-server:$version sard-agent:$version-pg18 ${E2e.POSTGRES_IMAGE} | gzip -1 > '$file'")
    }

    private fun host(vararg command: String): String {
        val process = ProcessBuilder(*command).redirectErrorStream(true).start()
        val output = process.inputStream.bufferedReader().readText()
        check(process.waitFor() == 0) { "${command.joinToString(" ")} failed: $output" }
        return output
    }
}
