// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import dev.sard.server.runs.Runs
import dev.sard.server.runs.SourceDraft
import dev.sard.server.runs.Sources
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** What a list is made of: its URL, how to create one more object, and the id of the newest one. */
private class Listing(
    val name: String,
    private val pathOf: () -> String,
    val create: () -> String,
) {
    val path: String get() = pathOf()
}

/** Rules "Списки — курсорная пагинация без пропусков и повторов" and the order of every list. */
@RestApiTest
class ListsApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired private val sources: Sources,
    @Autowired private val runs: Runs,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired private val mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val clock = world.clock

    @AfterTest
    fun `drop the tenants`() = world.close()

    /** A tenant of its own with an administrator; the agent, source and run counters are made when first needed. */
    private inner class Scene {
        val tenant = world.tenant()
        val admin = world.admin(tenant)
        val agent by lazy { world.agent(tenant) }
        val sourceId: UUID by lazy { newSource() }
        private var counter = 0

        fun newSource(): UUID {
            val draft = SourceDraft("source-${counter++}", agent.agentId, "files", "qa", """{"paths":["/etc"]}""")
            return sources.create(tenant, draft).id
        }

        /** A finished run of [sourceId], so that the next one may start; its id. */
        fun newRun(): String {
            val run = runs.start(tenant, sourceId).id
            world.forceRun(run, "succeeded")
            return run.toString()
        }

        fun newSnapshot(): String {
            val run = newRun()
            val step =
                world.jdbc.queryForObject(
                    "select id from run_steps where run_id = ?",
                    UUID::class.java,
                    UUID.fromString(run),
                )!!
            world.insertSnapshot(tenant, step, "snap-${counter++}")
            return world.jdbc.queryForObject("select id from snapshots where step_id = ?", String::class.java, step)!!
        }

        val listings: List<Listing> =
            listOf(
                Listing("agents", { "/api/v1/agents" }) { world.enroll(tenant).agentId.toString() },
                Listing("tokens", { "/api/v1/enrollment-tokens" }) { tokens.create(tenant).id.toString() },
                Listing("sources", { "/api/v1/sources" }) { newSource().toString() },
                Listing("runs", { "/api/v1/runs" }) { newRun() },
                Listing("snapshots", { "/api/v1/sources/$sourceId/snapshots" }) { newSnapshot() },
            )

        fun listing(name: String) = listings.first { it.name == name }

        /** The pages of [path] one after another: their ids. */
        fun walk(
            path: String,
            limit: Int? = null,
        ): List<List<String>> {
            val pages = mutableListOf<List<String>>()
            var cursor: String? = null
            do {
                val query = listOfNotNull(limit?.let { "limit=$it" }, cursor?.let { "cursor=$it" }).joinToString("&")
                val json = world.api.get(path + if (query.isEmpty()) "" else "?$query", admin).json
                pages += json.pluck("items", "id")
                cursor = json.path("nextCursor").takeIf { !it.isNull }?.asString()
            } while (cursor != null)
            return pages
        }
    }

    @Test
    fun `Список отдаёт по 50 элементов по умолчанию и последнюю страницу с nextCursor null`() {
        for (name in listOf("agents", "tokens", "sources", "runs", "snapshots")) {
            val scene = Scene()
            val listing = scene.listing(name)
            val created = List(120) { listing.create() }

            val pages = scene.walk(listing.path)

            assertEquals(listOf(50, 50, 20), pages.map { it.size }, name)
            assertEquals(created.toSet(), pages.flatten().toSet(), name)
        }
    }

    @Test
    fun `Страница размером 200 принимается`() {
        val scene = Scene()
        repeat(250) { tokens.create(scene.tenant) }

        val json = world.api.get("/api/v1/enrollment-tokens?limit=200", scene.admin).json

        assertEquals(200, json.path("items").size())
        assertTrue(!json.path("nextCursor").isNull)
    }

    @Test
    fun `Пустой список отдаёт пустую страницу без курсора`() {
        val json = world.api.get("/api/v1/sources", Scene().admin).json

        assertTrue(json.path("items").isEmpty)
        assertTrue(json.path("nextCursor").isNull)
    }

    @Test
    fun `Список по limit ровно равному числу элементов не даёт лишней пустой страницы`() {
        val scene = Scene()
        repeat(50) { tokens.create(scene.tenant) }

        val json = world.api.get("/api/v1/enrollment-tokens?limit=50", scene.admin).json

        assertEquals(50, json.path("items").size())
        assertTrue(json.path("nextCursor").isNull)
    }

    @Test
    fun `Вставки во время обхода не дают пропусков и повторов`() {
        for (name in listOf("tokens", "sources", "runs")) {
            val scene = Scene()
            val listing = scene.listing(name)
            val original = List(30) { listing.create() }
            val first = world.api.get("${listing.path}?limit=10", scene.admin).json
            repeat(5) { listing.create() }

            val rest = mutableListOf<String>()
            var cursor: String? = first.path("nextCursor").asString()
            while (cursor != null) {
                val json = world.api.get("${listing.path}?limit=10&cursor=$cursor", scene.admin).json
                rest += json.pluck("items", "id")
                cursor = json.path("nextCursor").takeIf { !it.isNull }?.asString()
            }

            val seen = first.pluck("items", "id") + rest
            assertEquals(seen.size, seen.toSet().size, name)
            assertTrue(seen.containsAll(original), name)
        }
    }

    @Test
    fun `Элементы с одинаковым временем упорядочены по id и не теряются на границе страницы`() {
        val scene = Scene()
        val created = List(3) { scene.listing("runs").create() }

        val pages = scene.walk("/api/v1/runs", limit = 1)

        assertEquals(3, pages.size)
        assertEquals(created.toSet(), pages.flatten().toSet())
        assertEquals(pages.flatten().sortedDescending(), pages.flatten())
    }

    @Test
    fun `Порядок списков`() {
        for (name in listOf("agents", "tokens", "sources", "runs", "snapshots")) {
            val scene = Scene()
            val listing = scene.listing(name)
            val order =
                List(3) {
                    clock.now = clock.now.plus(Duration.ofMinutes(1))
                    listing.create()
                }

            assertEquals(order.reversed(), scene.walk(listing.path).flatten(), name)
        }
    }

    @Test
    fun `Неверный параметр списка отклоняется ошибкой у этого параметра`() {
        val scene = Scene()
        repeat(2) { tokens.create(scene.tenant) }
        val tokenCursor =
            world.api
                .get("/api/v1/enrollment-tokens?limit=1", scene.admin)
                .json
                .path("nextCursor")
                .asString()
        val cases =
            listOf(
                "/api/v1/agents?limit=0" to "limit",
                "/api/v1/agents?limit=201" to "limit",
                "/api/v1/agents?limit=abc" to "limit",
                "/api/v1/sources?cursor=garbage" to "cursor",
                "/api/v1/runs?cursor=$tokenCursor" to "cursor",
                "/api/v1/runs?status=done" to "status",
                "/api/v1/agents?status=away" to "status",
                "/api/v1/enrollment-tokens?status=ACTIVE" to "status",
            )
        for ((path, field) in cases) {
            val response = world.api.get(path, scene.admin)

            assertEquals(422, response.status, path)
            assertEquals("validation_failed", response.code, path)
            assertEquals(listOf(field), response.errorFields(), path)
        }
    }

    @Test
    fun `Id в пути не в формате UUID отвечает 404`() {
        val response = world.api.get("/api/v1/sources/not-a-uuid", Scene().admin)

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
    }
}
