// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.auth.AdminLoginClockConfiguration
import dev.sard.server.pki.MovableClock
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.core.io.ClassPathResource
import tools.jackson.databind.ObjectMapper
import java.time.Clock
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val UUID_ = "0192f7a0-0000-7000-8000-000000000101"
private const val CSP =
    "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
        "font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; " +
        "form-action 'self'; frame-ancestors 'none'"
private const val IMMUTABLE = "public, max-age=31536000, immutable"

/** The @http scenarios of docs/specs/server/console-serving.feature against a server with the test console. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "SARD_ADMIN_PASSWORD=$CONSOLE_PASSWORD",
        "sard.console.location=$FIXTURE_CONSOLE",
    ],
)
@Import(TestcontainersConfiguration::class, AdminLoginClockConfiguration::class)
class ConsoleServingIntegrationTest(
    @Autowired private val mapper: ObjectMapper,
    @Autowired private val clock: Clock,
    @LocalServerPort port: Int,
) {
    private val http = ConsoleHttp(port)
    private val page = text("index.html")

    private fun text(path: String) = ClassPathResource("fixtures/console/$path").getContentAsString(Charsets.UTF_8)

    // ---- Корень и глубокие ссылки отдают страницу консоли ----

    @Test
    fun `Корень отдаёт страницу консоли`() {
        assertTrue(http.get("/").isConsolePage(page))
    }

    @Test
    fun `Глубокая ссылка после перезагрузки отдаёт страницу консоли`() {
        listOf(
            "/login",
            "/login?redirect=%2Fagents",
            "/agents",
            "/agents/",
            "/agents/$UUID_",
            "/sources/new",
            "/sources/$UUID_/edit",
            "/runs?status=failed",
            "/tokens",
            "/no-such-page/deeper",
            "/apiary",
        ).forEach { assertTrue(http.get(it).isConsolePage(page), it) }
    }

    @Test
    fun `Страница консоли отдаётся по имени файла index`() {
        assertTrue(http.get("/index.html").isConsolePage(page))
    }

    @Test
    fun `Глубокая ссылка отдаёт страницу консоли независимо от заголовка Accept`() {
        assertTrue(http.get("/agents", accept = "application/json").isConsolePage(page))
    }

    @Test
    fun `HEAD глубокой ссылки отвечает заголовками страницы без тела`() {
        val response = http.send("HEAD", "/agents/$UUID_")
        assertEquals(200, response.statusCode())
        assertEquals("text/html;charset=UTF-8", response.header("Content-Type"))
        assertEquals("no-cache", response.header("Cache-Control"))
        assertEquals("", response.body())
    }

    // ---- Файлы набора отдаются как есть, отсутствующий файл отвечает 404 ----

    @Test
    fun `Файл набора отдаётся со своим типом содержимого`() {
        mapOf(
            "/assets/index-Ab12Cd34.js" to "text/javascript",
            "/assets/index-Ab12Cd34.css" to "text/css",
            "/assets/plex-Ef56Gh78.woff2" to "font/woff2",
            "/favicon.svg" to "image/svg+xml",
        ).forEach { (path, type) ->
            val response = http.get(path)
            assertEquals(200, response.statusCode(), path)
            assertEquals(type, response.header("Content-Type"), path)
            assertEquals(text(path.removePrefix("/")), response.body(), path)
        }
    }

    private fun assertNotConsolePage(
        response: java.net.http.HttpResponse<String>,
        what: String,
    ) {
        assertFalse(response.body().contains(FIXTURE_PAGE_MARK), what)
        assertFalse((response.header("Content-Type") ?: "").startsWith("text/html"), what)
    }

    @Test
    fun `Отсутствующий файл отвечает 404, а не страницей консоли`() {
        listOf(
            "/assets/index-Zz00Zz00.js",
            "/assets/missing.css",
            "/assets/no-extension",
            "/robots.txt",
            "/agents/report.pdf",
            "/mockServiceWorker.js",
        ).forEach {
            val response = http.get(it)
            assertEquals(404, response.statusCode(), it)
            assertNotConsolePage(response, it)
        }
    }

    @Test
    fun `HEAD отсутствующего файла отвечает 404`() {
        assertEquals(404, http.send("HEAD", "/assets/index-Zz00Zz00.js").statusCode())
    }

    @Test
    fun `Файлы сервера вне набора консоли не отдаются`() {
        listOf(
            "/application.yaml",
            "/BOOT-INF/classes/application.yaml",
            "/META-INF/MANIFEST.MF",
            "/console/index.html",
        ).forEach {
            val response = http.get(it)
            assertEquals(404, response.statusCode(), it)
            assertFalse(response.body().contains("datasource"), it)
        }
    }

    @Test
    fun `Попытка выйти из каталога набора не отдаёт файл сервера`() {
        listOf(
            "/assets/../application.yaml",
            "/assets/%2e%2e/application.yaml",
            "/assets/%2e%2e%2f%2e%2e%2fapplication.yaml",
            "/%2e%2e/application.yaml",
        ).forEach {
            val response = http.get(it)
            assertTrue(response.statusCode() != 200, "$it ${response.statusCode()}")
            assertFalse(response.body().contains("datasource"), it)
        }
    }

    // ---- На путях консоли разрешены только GET и HEAD ----

    @Test
    fun `Изменяющий метод на пути консоли отвечает 405`() {
        listOf(
            "POST" to "/",
            "PUT" to "/agents",
            "DELETE" to "/agents/$UUID_",
            "DELETE" to "/favicon.svg",
            "PATCH" to "/index.html",
            "OPTIONS" to "/login",
            "POST" to "/assets/index-Ab12Cd34.js",
        ).forEach { (method, path) ->
            val response = http.send(method, path)
            assertEquals(405, response.statusCode(), "$method $path")
            assertEquals(
                setOf("GET", "HEAD"),
                response
                    .header("Allow")!!
                    .split(",")
                    .map { it.trim() }
                    .toSet(),
            )
            assertNotConsolePage(response, "$method $path")
        }
    }

    // ---- API, Actuator и OpenAPI не перекрываются консолью ----

    @Test
    fun `Неизвестный путь под api v1 без сессии по-прежнему отвечает 401`() {
        val response = http.get("/api/v1/no-such-resource")
        assertEquals(401, response.statusCode())
        assertEquals("unauthenticated", mapper.readTree(response.body()).path("code").asString())
    }

    @Test
    fun `Неизвестный путь под api v1 с сессией отвечает 404 в формате problem`() {
        val response = http.get("/api/v1/no-such-resource", cookie = http.signIn())
        assertEquals(404, response.statusCode())
        assertEquals("application/problem+json", response.header("Content-Type"))
        assertNotConsolePage(response, "api v1")
    }

    @Test
    fun `Путь под api вне api v1 отвечает 404 в формате problem`() {
        listOf("/api", "/api/", "/api/v2/agents").forEach {
            val response = http.get(it)
            assertEquals(404, response.statusCode(), it)
            assertEquals("application/problem+json", response.header("Content-Type"), it)
            assertNotConsolePage(response, it)
        }
    }

    @Test
    fun `Статус сервера отвечает JSON при наличии консоли`() {
        val response = http.get("/api/v1/status")
        assertEquals(200, response.statusCode())
        assertTrue(mapper.readTree(response.body()).has("version"))
    }

    @Test
    fun `Проверка здоровья отвечает JSON при наличии консоли`() {
        val response = http.get("/actuator/health")
        assertEquals(200, response.statusCode())
        assertEquals("UP", mapper.readTree(response.body()).path("status").asString())
    }

    @Test
    fun `Документ OpenAPI отвечает JSON при наличии консоли`() {
        val response = http.get("/v3/api-docs")
        assertEquals(200, response.statusCode())
        assertTrue(response.header("Content-Type")!!.startsWith("application/json"))
        assertTrue(mapper.readTree(response.body()).has("openapi"))
    }

    @Test
    fun `Неизвестный путь Actuator или OpenAPI отвечает 404 без страницы консоли`() {
        listOf("/actuator/no-such", "/actuator/env", "/v3/no-such", "/v3").forEach {
            val response = http.get(it)
            assertEquals(404, response.statusCode(), it)
            assertNotConsolePage(response, it)
        }
    }

    @Test
    fun `Путь API с параметром сегмента не отдаёт страницу консоли`() {
        listOf(
            "/api;x/v1/status",
            "/actuator;x/health",
            "/v3;x/api-docs",
            "/api/v1;x/status",
            "//api/v1/status",
            "/api//v1/status",
            "//actuator/health",
            "/;x/actuator/health",
            "//v3/api-docs",
            "///v3/api-docs",
        ).forEach {
            val response = http.get(it)
            assertFalse(response.body().contains(FIXTURE_PAGE_MARK), it)
            assertNull(response.header("Content-Security-Policy"), it)
        }
    }

    @Test
    fun `Путь API с лишним слэшем или параметром сегмента с cookie не отдаёт страницу консоли`() {
        val cookie = http.signIn()
        assertEquals(401, http.get("/api/v1/agents").statusCode())
        assertEquals(200, http.get("/api/v1/agents", cookie = cookie).statusCode())
        listOf("//api/v1/status", "/api//v1/status", "/api;x/v1/status", "/;x/api/v1/status").forEach {
            val response = http.get(it, cookie = cookie)
            assertFalse(response.header("Content-Type").orEmpty().startsWith("text/html"), it)
            assertFalse(response.body().contains(FIXTURE_PAGE_MARK), it)
            assertNull(response.header("Content-Security-Policy"), it)
        }
    }

    @Test
    fun `Документ OpenAPI не описывает пути консоли`() {
        val document = mapper.readTree(http.get("/v3/api-docs").body())
        document.path("paths").propertyNames().forEach { assertTrue(it.startsWith("/api/v1/"), it) }
        assertEquals(mapper.readTree(java.io.File("../web/src/api/openapi.json")), document)
    }

    // ---- Страница консоли не кэшируется, файлы с хэшем кэшируются навсегда ----

    @Test
    fun `Страница консоли отдаётся с Cache-Control no-cache`() {
        listOf("/", "/index.html", "/agents/$UUID_", "/favicon.svg").forEach {
            assertEquals("no-cache", http.get(it).header("Cache-Control"), it)
        }
    }

    @Test
    fun `Файл под assets отдаётся с долгим неизменяемым кэшем`() {
        listOf("/assets/index-Ab12Cd34.js", "/assets/index-Ab12Cd34.css", "/assets/plex-Ef56Gh78.woff2").forEach {
            assertEquals(IMMUTABLE, http.get(it).header("Cache-Control"), it)
        }
    }

    @Test
    fun `Ответ 404 на отсутствующий файл под assets не кэшируется навсегда`() {
        val response = http.get("/assets/index-Zz00Zz00.js")
        assertEquals(404, response.statusCode())
        assertFalse((response.header("Cache-Control") ?: "").contains("immutable"))
    }

    // ---- Ответы консоли несут заголовки безопасности ----

    @Test
    fun `Ответ консоли несёт заголовки безопасности`() {
        listOf("/", "/agents/$UUID_", "/assets/index-Ab12Cd34.js", "/favicon.svg").forEach {
            val response = http.get(it)
            assertEquals("nosniff", response.header("X-Content-Type-Options"), it)
            assertEquals("DENY", response.header("X-Frame-Options"), it)
            assertEquals("same-origin", response.header("Referrer-Policy"), it)
            assertEquals(CSP, response.header("Content-Security-Policy"), it)
        }
    }

    @Test
    fun `Политика содержимого страницы запрещает чужие источники и встраивание в фрейм`() {
        val response = http.get("/")
        assertEquals(CSP, response.header("Content-Security-Policy"))
        assertEquals("DENY", response.header("X-Frame-Options"))
    }

    // ---- Консоль открывается без сессии и не трогает сессию ----

    @Test
    fun `Корень без сессии отдаёт страницу консоли, а не 401`() {
        val response = http.get("/")
        assertTrue(response.isConsolePage(page))
        assertNull(response.header("Set-Cookie"))
    }

    @Test
    fun `Глубокая ссылка с недействующей cookie отдаёт страницу консоли без Set-Cookie`() {
        val response = http.get("/agents", cookie = "forged-session-id")
        assertTrue(response.isConsolePage(page))
        assertNull(response.header("Set-Cookie"))
    }

    @Test
    fun `Файл набора с действующей сессией отдаётся без Set-Cookie`() {
        val response = http.get("/assets/index-Ab12Cd34.js", cookie = http.signIn())
        assertEquals(200, response.statusCode())
        assertNull(response.header("Set-Cookie"))
    }

    @Test
    fun `Запрос к консоли не продлевает сессию`() {
        val movable = clock as MovableClock
        movable.now = Instant.parse("2026-10-01T12:00:00Z")
        val session = http.signIn()
        movable.now = Instant.parse("2026-10-01T23:00:00Z")
        http.get("/agents", cookie = session)
        movable.now = Instant.parse("2026-10-02T00:00:00Z")
        assertEquals(401, http.get("/api/v1/session", cookie = session).statusCode())
    }

    @Test
    fun `API без сессии отвечает 401 при наличии консоли`() {
        assertEquals(401, http.get("/api/v1/agents").statusCode())
    }
}
