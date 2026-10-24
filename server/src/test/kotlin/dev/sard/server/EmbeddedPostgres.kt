// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import org.testcontainers.postgresql.PostgreSQLContainer
import org.testcontainers.utility.DockerImageName
import java.io.File

/**
 * The PostgreSQL of deploy/docker-compose.yml for the tests: the image pinned in deploy/postgres.env and a database
 * created by initdb with the same arguments (DB1), so a test database looks like the one of a new installation.
 */
object EmbeddedPostgres {
    private val pin: Map<String, String> by lazy {
        val path = requireNotNull(System.getProperty("sard.test.postgres-pin-file")) { "run the tests with Gradle" }
        File(path)
            .readLines()
            .map { it.trim() }
            .filter { it.isNotEmpty() && !it.startsWith("#") }
            .associate { it.substringBefore("=") to it.substringAfter("=").removeSurrounding("\"") }
    }

    val image: String get() = pin.getValue("SARD_POSTGRES_IMAGE")

    val initdbArgs: String get() = pin.getValue("SARD_POSTGRES_INITDB_ARGS")

    /** A container as the compose file creates it; [image] may name another PostgreSQL (an external one). */
    fun container(image: String = this.image): PostgreSQLContainer =
        PostgreSQLContainer(DockerImageName.parse(image).asCompatibleSubstituteFor("postgres"))
            .withEnv("POSTGRES_INITDB_ARGS", initdbArgs)
            .withEnv("TZ", "UTC")
}
