// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.downloads

import dev.sard.server.TestcontainersConfiguration
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.test.context.DynamicPropertyRegistry
import org.springframework.test.context.DynamicPropertySource
import tools.jackson.databind.json.JsonMapper
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.nio.file.Path
import java.security.MessageDigest
import java.util.Properties
import kotlin.io.path.readText
import kotlin.io.path.writeText
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val BUILD_INFO = "/META-INF/build-info.properties"

/** The version this build of the server reports (BuildProperties), so the fixture release matches it. */
private val SERVER_VERSION: String =
    Properties()
        .apply { AgentDownloadsIntegrationTest::class.java.getResourceAsStream(BUILD_INFO)!!.use { load(it) } }
        .getProperty("build.version")

private val RELEASE: Path =
    AgentPackageFixture.tempRelease(SERVER_VERSION).also {
        // In the directory, not in the release: never served.
        it.resolve("notes.txt").writeText("not a release file\n")
        it.resolve("SHA256SUMS.minisig").writeText("untrusted comment: test signature\n")
    }

/**
 * The server hands out the agent packages of its own version without a session
 * (docs/adr/0041-agent-release.md): `/downloads/agent/<file>`.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.agent-packages.enabled=true"],
)
@Import(TestcontainersConfiguration::class)
class AgentDownloadsIntegrationTest(
    @LocalServerPort private val port: Int,
) {
    companion object {
        @JvmStatic
        @DynamicPropertySource
        fun packages(registry: DynamicPropertyRegistry) {
            registry.add("sard.agent-packages.dir") { RELEASE.toString() }
        }
    }

    private val http = HttpClient.newHttpClient()

    private fun get(
        file: String,
        vararg headers: String,
        method: String = "GET",
    ): HttpResponse<ByteArray> {
        val request = HttpRequest.newBuilder(URI.create("http://localhost:$port/downloads/agent/$file"))
        if (headers.isNotEmpty()) request.headers(*headers)
        val built = request.method(method, HttpRequest.BodyPublishers.noBody()).build()
        return http.send(built, HttpResponse.BodyHandlers.ofByteArray())
    }

    private fun sha256(bytes: ByteArray) = MessageDigest.getInstance("SHA-256").digest(bytes).toHexString()

    private fun HttpResponse<*>.header(name: String) = headers().firstValue(name).orElse(null)

    @Test
    fun `the manifest is served without a session`() {
        val response = get("manifest.json")
        assertEquals(200, response.statusCode())
        assertEquals("application/json", response.header("Content-Type"))
        assertEquals("no-cache", response.header("Cache-Control"))
        assertEquals("\"${sha256(response.body())}\"", response.header("ETag"))
        val manifest = JsonMapper.builder().build().readTree(response.body())
        assertEquals(SERVER_VERSION, manifest["version"].asString())
    }

    @Test
    fun `every package matches its sum in the manifest and in SHA256SUMS`() {
        val manifest = JsonMapper.builder().build().readTree(get("manifest.json").body())
        val sums = String(get("SHA256SUMS").body())
        for (artifact in manifest["artifacts"]) {
            val file = artifact["file"].asString()
            val response = get(file)
            assertEquals(200, response.statusCode(), file)
            assertEquals(artifact["sha256"].asString(), sha256(response.body()), file)
            assertTrue(sums.contains("${artifact["sha256"].asString()}  $file\n"), file)
            assertEquals("public, max-age=31536000, immutable", response.header("Cache-Control"), file)
            assertEquals("bytes", response.header("Accept-Ranges"), file)
        }
        val deb = get(AgentPackageFixture.DEB.format(SERVER_VERSION))
        assertEquals("application/vnd.debian.binary-package", deb.header("Content-Type"))
    }

    @Test
    fun `SHA256SUMS and its signature are the files in the directory`() {
        assertEquals(RELEASE.resolve("SHA256SUMS").readText(), String(get("SHA256SUMS").body()))
        val signature = get("SHA256SUMS.minisig")
        assertEquals(200, signature.statusCode())
        assertEquals("no-cache", signature.header("Cache-Control"))
    }

    @Test
    fun `a range of a package is served`() {
        val deb = AgentPackageFixture.DEB.format(SERVER_VERSION)
        val response = get(deb, "Range", "bytes=10-19")
        assertEquals(206, response.statusCode())
        assertEquals("bytes 10-19/${AgentPackageFixture.SIZE}", response.header("Content-Range"))
        assertContentEquals(AgentPackageFixture.content(7).copyOfRange(10, 20), response.body())
    }

    @Test
    fun `an unchanged package is not sent again`() {
        val deb = AgentPackageFixture.DEB.format(SERVER_VERSION)
        val etag = get(deb).header("ETag")
        assertEquals(304, get(deb, "If-None-Match", etag).statusCode())
    }

    @Test
    fun `HEAD tells the size without the body`() {
        val response = get(AgentPackageFixture.TAR.format(SERVER_VERSION), method = "HEAD")
        assertEquals(200, response.statusCode())
        assertEquals("${AgentPackageFixture.SIZE}", response.header("Content-Length"))
        assertEquals(0, response.body().size)
    }

    @Test
    fun `only files of the release are served`() {
        for (path in listOf("notes.txt", "", "absent.deb", "%2e%2e/%2e%2e/etc/passwd", "..%2Fmanifest.json")) {
            val status = get(path).statusCode()
            assertTrue(status in 400..499, "$path: $status")
        }
    }
}

/** With downloads switched off the path does not exist. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = [
        "spring.grpc.server.port=0",
        "sard.agent-packages.enabled=false",
        "sard.agent-packages.dir=/nonexistent",
    ],
)
@Import(TestcontainersConfiguration::class)
class AgentDownloadsOffIntegrationTest(
    @LocalServerPort private val port: Int,
) {
    @Test
    fun `nothing is served when downloads are off`() {
        val response =
            HttpClient.newHttpClient().send(
                HttpRequest.newBuilder(URI.create("http://localhost:$port/downloads/agent/manifest.json")).build(),
                HttpResponse.BodyHandlers.discarding(),
            )
        assertEquals(404, response.statusCode())
    }
}
