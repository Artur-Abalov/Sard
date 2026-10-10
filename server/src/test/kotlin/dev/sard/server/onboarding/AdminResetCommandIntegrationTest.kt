// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.auth.JdbcAdministrators
import org.junit.jupiter.api.io.TempDir
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.testcontainers.postgresql.PostgreSQLContainer
import java.nio.file.Files
import java.nio.file.Path
import java.time.Instant
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private const val HASH = "\$argon2id\$v=19\$m=19456,t=2,p=1\$c2FsdA\$aGFzaA"

/**
 * Rule "Команда admin-reset возвращает доступ только тому, у кого есть хост сервера" (@command): the jar's own
 * main, as a process against the database of a Testcontainers PostgreSQL.
 */
@SpringBootTest(properties = ["spring.grpc.server.port=0", "server.port=0"])
@Import(TestcontainersConfiguration::class)
class AdminResetCommandIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val postgres: PostgreSQLContainer,
) {
    @TempDir
    lateinit var tmp: Path

    private val administrators = JdbcAdministrators(jdbc)

    @BeforeTest
    fun `an administrator and a confirmed CA step`() {
        administrators.create(HASH, Instant.parse("2026-10-09T12:00:00Z"))
        JdbcOnboardingSteps(jdbc).confirmCa(Instant.parse("2026-10-09T12:00:00Z"))
    }

    @AfterTest
    fun `forget the state`() {
        jdbc.update("delete from administrators")
        jdbc.update("delete from onboarding_steps")
    }

    private class Result(
        val exit: Int,
        val output: String,
    )

    private fun command(
        vararg args: String,
        url: String = postgres.jdbcUrl,
    ): Result {
        val java = Path.of(System.getProperty("java.home"), "bin", "java").toString()
        val builder =
            ProcessBuilder(
                java,
                "-cp",
                System.getProperty("java.class.path"),
                "dev.sard.server.SardServerApplicationKt",
                *args,
            ).redirectErrorStream(true)
        builder.environment().apply {
            put("SARD_DB_URL", url)
            put("SARD_DB_USER", postgres.username)
            put("SARD_DB_PASSWORD", postgres.password)
            put("SARD_PKI_DIR", tmp.resolve("pki").toString())
            put("SARD_SELF_DIR", tmp.resolve("self").toString())
        }
        val process = builder.start()
        val output = process.inputStream.readAllBytes().toString(Charsets.UTF_8)
        check(process.waitFor(120, TimeUnit.SECONDS)) { "the command did not finish" }
        return Result(process.exitValue(), output)
    }

    private fun filesUnder(root: Path): List<String> =
        if (Files.notExists(root)) {
            emptyList()
        } else {
            Files.walk(root).use { s -> s.map { "$it ${Files.getLastModifiedTime(it)}" }.sorted().toList() }
        }

    @Test
    fun `admin-reset удаляет пароль администратора и выходит с кодом 0`() {
        val result = command("admin-reset")

        assertEquals(0, result.exit, result.output)
        assertTrue("docker compose restart server" in result.output, result.output)
        assertNull(administrators.hash())
        assertEquals(1, jdbc.queryForObject("select count(*) from onboarding_steps where step = 'ca'", Int::class.java))
    }

    @Test
    fun `admin-reset не запускает сервер, не создаёт каталог CA и канал`() {
        Files.createDirectories(tmp.resolve("pki/ca"))
        Files.writeString(tmp.resolve("pki/ca/ca.key"), "key")
        Files.createDirectories(tmp.resolve("self"))
        Files.writeString(tmp.resolve("self/enroll-token"), "token")
        val before = filesUnder(tmp)
        val tokens = jdbc.queryForObject("select count(*) from enrollment_tokens", Int::class.java)

        val result = command("admin-reset")

        assertEquals(0, result.exit, result.output)
        assertFalse("Tomcat" in result.output || "Started" in result.output || "gRPC" in result.output, result.output)
        assertEquals(before, filesUnder(tmp))
        assertEquals(tokens, jdbc.queryForObject("select count(*) from enrollment_tokens", Int::class.java))
    }

    @Test
    fun `admin-reset без администратора выходит с кодом 0 и ничего не меняет`() {
        administrators.remove()

        val result = command("admin-reset")

        assertEquals(0, result.exit, result.output)
        assertTrue("not set" in result.output, result.output)
        assertEquals(1, jdbc.queryForObject("select count(*) from onboarding_steps", Int::class.java))
    }

    @Test
    fun `admin-reset при недоступной базе выходит с кодом 1, называет базу и ничего не меняет`() {
        val result = command("admin-reset", url = "jdbc:postgresql://localhost:1/none")

        assertEquals(1, result.exit, result.output)
        assertTrue("jdbc:postgresql://localhost:1/none" in result.output, result.output)
        assertTrue("nothing was changed" in result.output, result.output)
        assertEquals(HASH, administrators.hash())
    }

    @Test
    fun `Неизвестная команда выходит с кодом 2 и называет admin-reset`() {
        val result = command("admin-rest")

        assertEquals(2, result.exit, result.output)
        assertTrue("admin-reset" in result.output, result.output)
        assertEquals(HASH, administrators.hash())
    }

    @Test
    fun `Вывод admin-reset не содержит хэш пароля`() {
        val result = command("admin-reset")

        assertFalse("argon2id" in result.output || HASH in result.output, result.output)
    }
}
