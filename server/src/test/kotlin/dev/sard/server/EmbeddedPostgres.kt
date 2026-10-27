// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import com.github.dockerjava.api.command.InspectContainerResponse
import dev.sard.server.database.Preparation
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

    /**
     * A container as [container] makes it, and with the database prepared once it runs (DB2): db-init's bootstrap, the
     * passwords of the login roles and the migrations. A server connects to it as sard_app ([appUser]); the superuser
     * of the container stays the tests' way to do what sard_app may not.
     */
    fun prepared(image: String = this.image): PostgreSQLContainer =
        PreparedPostgres(DockerImageName.parse(image).asCompatibleSubstituteFor("postgres"))
            .withEnv("POSTGRES_INITDB_ARGS", initdbArgs)
            .withEnv("TZ", "UTC")

    const val APP_USER = "sard_app"

    val appPassword: String get() = Preparation.password(APP_USER)
}

private class PreparedPostgres(
    image: DockerImageName,
) : PostgreSQLContainer(image) {
    override fun containerIsStarted(containerInfo: InspectContainerResponse) {
        super.containerIsStarted(containerInfo)
        TestAdmin.register(this)
        createConnection("").use(Preparation::bootstrap)
        Preparation.migrate(jdbcUrl)
    }
}
