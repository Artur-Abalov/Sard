// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.testcontainers.containers.GenericContainer
import org.testcontainers.containers.wait.strategy.Wait
import org.testcontainers.images.builder.Transferable
import org.testcontainers.utility.DockerImageName

/**
 * The stand's SFTP server (test/e2e/sftp, ADR 0047) on the environment's network as [ALIAS]:
 * OpenSSH with public keys only. [USER] writes below [WRITABLE]; [READ_ONLY] is read-only to it.
 */
internal class SftpServer(
    private val sardEnv: SardEnvironment,
) {
    /** The server's container, for [Interruptions.stop] and [Interruptions.start]. */
    val container: GenericContainer<*> =
        GenericContainer<Nothing>(DockerImageName.parse(E2e.sftpImage)).apply {
            withNetwork(sardEnv.dockerNetwork)
            withNetworkAliases(ALIAS)
            waitingFor(Wait.forLogMessage(".*Server listening on 0\\.0\\.0\\.0 port 22.*", 1))
        }

    /** Starts the server; tracked by the environment. */
    fun start(): SftpServer = apply { sardEnv.track(ALIAS, container).start() }

    private val authorized = linkedSetOf<String>()

    /** Lets [publicKey] (an OpenSSH public key line) log in as [USER], besides the keys already let in. */
    fun authorize(publicKey: String) {
        authorized += publicKey.trim()
        writeAuthorizedKeys()
    }

    /** Takes [publicKey] off [USER]'s authorized keys. */
    fun revoke(publicKey: String) {
        authorized -= publicKey.trim()
        writeAuthorizedKeys()
    }

    private fun writeAuthorizedKeys() {
        val content = authorized.joinToString("") { "$it\n" }
        container.copyFileToContainer(Transferable.of(content, TarFiles.READABLE), "/etc/ssh/authorized_keys/$USER")
    }

    /** The server's host key as a known_hosts line for [ALIAS]: what the operator verifies and trusts. */
    fun knownHostsLine(): String {
        val key = container.copyFileFromContainer(HOST_KEY) { String(it.readAllBytes()) }.trim().split(' ')
        return "$ALIAS ${key[0]} ${key[1]}\n"
    }

    /** Runs [command] as root on the server (the storage admin's shell); fails unless it exits 0. */
    fun exec(vararg command: String) {
        val result = container.execInContainer(*command)
        check(result.exitCode == 0) { "${command.joinToString(" ")} exited ${result.exitCode}: ${result.stderr}" }
    }

    /** The restic repository string of [directory] on this server. */
    fun url(directory: String): String = "sftp:$USER@$ALIAS:$directory"

    companion object {
        const val ALIAS = "sftp"
        const val USER = "sard"
        const val WRITABLE = "/srv/sftp/repo"
        const val READ_ONLY = "/srv/sftp/ro"
        private const val HOST_KEY = "/etc/ssh/ssh_host_ed25519_key.pub"
    }
}
