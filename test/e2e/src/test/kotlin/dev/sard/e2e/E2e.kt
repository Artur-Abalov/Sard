// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.nio.file.Files
import java.nio.file.Path

/** Settings `make e2e` passes to the test JVM (test/e2e/build.gradle.kts). */
internal object E2e {
    val serverImage: String get() = property("e2e.serverImage")
    /** The agent of the release packages (`make package`), the one every class runs but T3's. */
    val agentImage: String get() = property("e2e.agentImage")

    /** The release agent on postgres:18 (deploy/agent/Dockerfile, BASE_IMAGE): the compose file's sard-self (F6). */
    val agentPgImage: String get() = property("e2e.agentPgImage")

    /** The stand's agent (`GO_TAGS=e2e`, ADR 0036): the release agent plus `e2e-slow`, for T3 only. */
    val standAgentImage: String get() = property("e2e.standAgentImage")

    /**
     * The release agent on the official `postgres:<major>` image (F1 ПГ20), whose pg_dump, psql and
     * pg_dumpall are of that major version: 18 and 14.
     */
    fun pgAgentImage(major: Int): String = property("e2e.pg${major}AgentImage")

    /** The release agent with the Docker CLI a verifier starts its sandboxes with (test/e2e/agent/Dockerfile.verifier, V5). */
    val verifierImage: String get() = property("e2e.verifierImage")

    /** The stand's SFTP server (test/e2e/sftp, ADR 0047). */
    val sftpImage: String get() = property("e2e.sftpImage")

    /** The machine of the recovery drill (test/e2e/recovery-vm): Docker in Docker with bash, coreutils and curl (F7, Р39). */
    val recoveryVmImage: String get() = property("e2e.recoveryVmImage")

    /** deploy/docker-compose.yml: the installation the drill's old machine runs. */
    val composeFile: Path get() = Path.of(property("e2e.composeFile"))

    /** docs/operator/12-server-recovery.md: the page the drill follows. */
    val recoveryPage: Path get() = Path.of(property("e2e.recoveryPage"))

    /** Where the drill writes `result.json` and `result.md` (test/e2e/build/recovery-drill). */
    val recoveryDrillDir: Path get() = Path.of(property("e2e.recoveryDrillDir"))

    /** The version both images were built with (`VERSION` in the Makefile). */
    val version: String get() = property("e2e.version")

    val logsDir: Path get() = Path.of(property("e2e.logsDir"))

    /** restic's pinned version: `version=` in agent/internal/restic/restic-version (ADR 0017). */
    val resticVersion: String
        get() =
            Files
                .readAllLines(Path.of(property("e2e.resticVersionFile")))
                .single { it.startsWith("version=") }
                .removePrefix("version=")

    /** deploy/postgres.env (DB1): the server's database image and its initdb arguments, as the compose file has them. */
    private val postgresPin: Map<String, String>
        get() =
            Files
                .readAllLines(Path.of(property("e2e.postgresPinFile")))
                .map { it.trim() }
                .filter { it.isNotEmpty() && !it.startsWith("#") }
                .associate { it.substringBefore("=") to it.substringAfter("=").removeSurrounding("\"") }

    /** The server's database, pinned by digest. */
    val postgresImage: String get() = postgresPin.getValue("SARD_POSTGRES_IMAGE")

    /** [postgresImage] without its digest: the name an offline archive carries it under (docs/operator/11-offline.md). */
    val postgresTag: String get() = postgresImage.substringBefore("@")

    val postgresInitdbArgs: String get() = postgresPin.getValue("SARD_POSTGRES_INITDB_ARGS")

    /** The server a source of the postgresql plugin backs up: the official image of [major], pinned in test/images.env. */
    fun pgServerImage(major: Int): String = testImage("postgres:$major")

    /** The reference test/images.env pins for [tag] (SC1): a test never pulls by tag, an unpinned one fails. */
    fun testImage(tag: String): String =
        Files
            .readAllLines(Path.of(property("e2e.testImagesFile")))
            .map { it.trim() }
            .filter { it.isNotEmpty() && !it.startsWith("#") }
            .map { it.substringAfter("=") }
            .firstOrNull { it.startsWith("$tag@") }
            ?: error("$tag is not pinned in test/images.env")

    private fun property(name: String): String =
        requireNotNull(System.getProperty(name)) { "system property $name is not set: run the tests with `make e2e`" }
}
