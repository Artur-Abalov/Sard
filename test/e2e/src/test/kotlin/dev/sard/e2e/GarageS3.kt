// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.testcontainers.containers.GenericContainer
import org.testcontainers.containers.wait.strategy.Wait
import org.testcontainers.images.builder.Transferable
import org.testcontainers.utility.DockerImageName
import java.security.SecureRandom
import java.util.HexFormat

/**
 * The stand's S3-compatible storage (ADR 0047): one Garage node on the environment's network as
 * [ALIAS]. Buckets and access keys are made with the `garage` CLI inside its container, as an
 * operator of Garage does; a key is given read, or read and write, on one bucket. Secret keys are
 * registered with the environment, so no log shows them.
 */
internal class GarageS3(
    private val sardEnv: SardEnvironment,
) {
    /** An access key of this storage, as an agent's env_file passes it to restic. */
    class Key(
        val id: String,
        val secret: String,
    ) {
        /** The env_file of a repository on this storage (agent/internal/restic/env.go). */
        fun envFile(): String = env().entries.joinToString("") { (name, value) -> "$name=$value\n" }

        /** The same variables, for a restic run outside the agent (`restic restore` on the host). */
        fun env(): Map<String, String> = mapOf("AWS_ACCESS_KEY_ID" to id, "AWS_SECRET_ACCESS_KEY" to secret, "AWS_DEFAULT_REGION" to REGION)

        override fun toString() = "Key(id=$id)"
    }

    /** The node's container, for [Interruptions.stop] and [Interruptions.start]. */
    val container: GenericContainer<*> =
        GenericContainer<Nothing>(DockerImageName.parse(IMAGE)).apply {
            withNetwork(sardEnv.dockerNetwork)
            withNetworkAliases(ALIAS)
            withCopyToContainer(Transferable.of(config(randomHex())), CONFIG)
            waitingFor(Wait.forLogMessage(".*S3 API server listening.*", 1))
        }

    /** Starts the node and gives it its one-node layout; tracked by the environment. */
    fun start(): GarageS3 {
        sardEnv.track(ALIAS, container).start()
        val node = garage("node", "id", "-q").substringBefore('@').trim()
        garage("layout", "assign", "-z", "dc1", "-c", "1G", node)
        garage("layout", "apply", "--version", "1")
        return this
    }

    /** Creates bucket [name]. */
    fun bucket(name: String): String = name.also { garage("bucket", "create", it) }

    /** Creates key [name] with read access to [bucket], and write access if [write]. */
    fun key(
        name: String,
        bucket: String,
        write: Boolean,
    ): Key {
        val out = garage("key", "create", name)
        val key = Key(field(out, "Key ID"), field(out, "Secret key"))
        sardEnv.secret(key.secret)
        allow(bucket, name, write)
        return key
    }

    /** Gives key [name] read access to [bucket], and write access if [write]. */
    fun allow(
        bucket: String,
        name: String,
        write: Boolean,
    ) {
        garage("bucket", "allow", "--read", *(if (write) arrayOf("--write") else emptyArray()), bucket, "--key", name)
    }

    /** Takes back key [name]'s write access to [bucket] (or all access, unless [write]). */
    fun deny(
        bucket: String,
        name: String,
        write: Boolean,
    ) {
        garage("bucket", "deny", *(if (write) arrayOf("--write") else arrayOf("--read", "--write")), bucket, "--key", name)
    }

    /** The restic repository string of [path] in [bucket], as the agent on the network reaches it. */
    fun url(
        bucket: String,
        path: String,
    ): String = "s3:http://$ALIAS:$S3_PORT/$bucket/$path"

    private fun garage(vararg args: String): String {
        val result = container.execInContainer("/garage", *args)
        check(result.exitCode == 0) { "garage ${args.first()} ${args.getOrNull(1)} exited ${result.exitCode}: ${result.stderr}" }
        return result.stdout
    }

    private fun field(
        output: String,
        name: String,
    ): String = checkNotNull(Regex("^$name:\\s+(\\S+)\\s*$", RegexOption.MULTILINE).find(output)) { "no $name in `garage key create`" }.groupValues[1]

    // 0.0.0.0, not [::]: Docker networks have no IPv6 by default, and Garage stops at start (phase 1 of F2).
    private fun config(rpcSecret: String) =
        """
        metadata_dir = "/var/lib/garage/meta"
        data_dir = "/var/lib/garage/data"
        db_engine = "sqlite"
        replication_factor = 1
        rpc_bind_addr = "0.0.0.0:3901"
        rpc_public_addr = "127.0.0.1:3901"
        rpc_secret = "$rpcSecret"

        [s3_api]
        s3_region = "$REGION"
        api_bind_addr = "0.0.0.0:$S3_PORT"
        """.trimIndent() + "\n"

    companion object {
        /** Garage v2.1.0 (Docker Hub; the cloud workaround of OQ-132 pulls it via a mirror and tags it). */
        const val IMAGE = "dxflrs/garage:v2.1.0"
        const val ALIAS = "garage"
        const val S3_PORT = 3900
        const val REGION = "garage"
        private const val CONFIG = "/etc/garage.toml"
        private val random = SecureRandom()

        private fun randomHex() = HexFormat.of().formatHex(ByteArray(32).also(random::nextBytes))
    }
}
