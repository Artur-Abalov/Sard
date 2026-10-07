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

    /** The stand's agent (`GO_TAGS=e2e`, ADR 0036): the release agent plus `e2e-slow`, for T3 only. */
    val standAgentImage: String get() = property("e2e.standAgentImage")

    /**
     * The release agent on the official `postgres:<major>` image (F1 ПГ20), whose pg_dump, psql and
     * pg_dumpall are of that major version: 18 and 14.
     */
    fun pgAgentImage(major: Int): String = property("e2e.pg${major}AgentImage")

    /** The stand's SFTP server (test/e2e/sftp, ADR 0047). */
    val sftpImage: String get() = property("e2e.sftpImage")

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

    const val POSTGRES_IMAGE = "postgres:18-alpine"

    /** The server a source of the postgresql plugin backs up: the official image of [major]. */
    fun pgServerImage(major: Int): String = "postgres:$major"

    private fun property(name: String): String =
        requireNotNull(System.getProperty(name)) { "system property $name is not set: run the tests with `make e2e`" }
}
