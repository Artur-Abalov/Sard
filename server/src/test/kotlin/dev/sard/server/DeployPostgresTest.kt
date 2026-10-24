// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val DIGEST = Regex("""^postgres:(\d+\.\d+)-[a-z]+@sha256:[0-9a-f]{64}$""")

/** The server's image written out: `postgres:<version>-alpine`, with a digest or without. */
private val ALPINE_TAG = Regex("""postgres:\d+(\.\d+)?-alpine(@sha256:[0-9a-f]{64})?""")

/**
 * One PostgreSQL for every installation (DB1): deploy/postgres.env chooses it, and the copies a release ships on
 * their own (the compose file, the env example) and every script, workflow and test that starts the server's
 * database say the same.
 */
class DeployPostgresTest {
    private val pin: Map<String, String> =
        File("../deploy/postgres.env")
            .readLines()
            .map { it.trim() }
            .filter { it.isNotEmpty() && !it.startsWith("#") }
            .associate { it.substringBefore("=") to it.substringAfter("=").removeSurrounding("\"") }

    private val image = pin.getValue("SARD_POSTGRES_IMAGE")
    private val agentBase = pin.getValue("SARD_AGENT_PG_BASE_IMAGE")

    private fun lines(path: String) = File("../$path").readLines()

    /** The lines of the compose service [service] (its keys are indented four spaces). */
    private fun service(service: String) =
        lines("deploy/docker-compose.yml")
            .dropWhile { it != "  $service:" }
            .drop(1)
            .takeWhile { !it.matches(Regex("""^  \S.*""")) }

    @Test
    fun `both images are pinned by digest and are of one PostgreSQL version`() {
        val server = checkNotNull(DIGEST.matchEntire(image)) { image }.groupValues[1]
        val agent = checkNotNull(DIGEST.matchEntire(agentBase)) { agentBase }.groupValues[1]

        assertEquals(server, agent, "the agent's pg_dump is of the server's version")
    }

    @Test
    fun `the compose file starts the pinned image with the initdb arguments and the mounted settings`() {
        val postgres = service("postgres").map { it.trim() }

        assertTrue("image: \${SARD_POSTGRES_IMAGE:-$image}" in postgres, postgres.toString())
        val initdb = "POSTGRES_INITDB_ARGS: ${pin.getValue("SARD_POSTGRES_INITDB_ARGS")}"
        assertTrue(initdb in postgres, postgres.toString())
        assertTrue("TZ: UTC" in postgres, postgres.toString())
        assertTrue(postgres.any { "config_file=/etc/postgresql/postgresql.conf" in it }, postgres.toString())
        assertTrue("target: /etc/postgresql/postgresql.conf" in postgres, postgres.toString())
    }

    @Test
    fun `the mounted settings are the small profile in UTC`() {
        val conf =
            lines("deploy/docker-compose.yml")
                .dropWhile { it != "  postgresql-conf:" }
                .takeWhile { it.isNotBlank() }
                .map { it.trim() }
        val keys = conf.filter { " = " in it }.associate { it.substringBefore(" = ") to it.substringAfter(" = ") }

        val profile =
            listOf("max_connections", "shared_buffers", "effective_cache_size", "work_mem", "maintenance_work_mem") +
                listOf("max_wal_size", "log_min_duration_statement", "log_lock_waits", "log_autovacuum_min_duration") +
                listOf("track_io_timing", "listen_addresses")
        assertTrue(keys.keys.containsAll(profile), (profile - keys.keys).toString())
        assertEquals("replica", keys["wal_level"])
        assertEquals("'UTC'", keys["timezone"])
        assertEquals("'UTC'", keys["log_timezone"])
    }

    @Test
    fun `the env example names the pinned image`() {
        assertTrue("# SARD_POSTGRES_IMAGE=$image" in lines("deploy/.env.example"))
    }

    @Test
    fun `nothing else starts the server's database from an image of its own`() {
        val roots = listOf("deploy", "scripts", ".github", "test/e2e/src", "server/src/test", "docs/operator")
        val tree = (roots + "docs/en").flatMap { File("../$it").walkTopDown().filter(File::isFile).toList() }
        val files = tree + File("../Makefile") + File("../docs/dependencies.md")
        val written =
            files.flatMap { file ->
                file.readLines().flatMap { line -> ALPINE_TAG.findAll(line).map { "${file.path}: ${it.value}" } }
            }

        // The tag alone is what an offline installation names (docs/operator/11-offline.md).
        val pinned = listOf(image, image.substringBefore("@"))
        assertEquals(emptyList(), written.filterNot { line -> pinned.any { line.endsWith(": $it") } })
    }

    /**
     * The F1 e2e agents (test/e2e/agent/Dockerfile.postgres) are built on the database they back up, not on the pin.
     */
    @Test
    fun `the -pg18 agent image is built on the pinned base`() {
        val literal =
            listOf(".github/workflows/release.yml", "deploy/docker-compose.build.yml", "Makefile").flatMap { path ->
                lines(path)
                    .filter { "BASE_IMAGE" in it && "postgres:" in it && "Dockerfile.postgres" !in it }
                    .map { "$path: ${it.trim()}" }
            }

        assertEquals(emptyList(), literal)
    }
}
