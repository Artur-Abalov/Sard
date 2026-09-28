// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.nio.file.Files
import java.nio.file.Path

/** Settings `make e2e` passes to the test JVM (test/e2e/build.gradle.kts). */
internal object E2e {
    val serverImage: String get() = property("e2e.serverImage")
    val agentImage: String get() = property("e2e.agentImage")

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

    private fun property(name: String): String =
        requireNotNull(System.getProperty(name)) { "system property $name is not set: run the tests with `make e2e`" }
}
