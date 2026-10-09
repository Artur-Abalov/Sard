// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentToken
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.enrollment.TokenServiceTestConfiguration
import dev.sard.server.enrollment.deleteEnrollmentTenantData
import dev.sard.server.enrollment.insertTenant
import dev.sard.server.extension.TenantResolver
import dev.sard.server.onboarding.ConfirmedCaStep
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures.resource
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.boot.testcontainers.service.connection.ServiceConnection
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.DynamicPropertyRegistry
import org.springframework.test.context.DynamicPropertySource
import org.testcontainers.postgresql.PostgreSQLContainer
import org.testcontainers.utility.DockerImageName
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.PosixFilePermissions
import java.sql.DriverManager
import java.sql.SQLException
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

private val T0: Instant = Instant.parse("2026-10-08T12:00:00Z")
private val DEFAULT_TENANT = TenantResolver.DEFAULT_TENANT_ID
private val MARGIN: Duration = Duration.ofMinutes(10)
private val FORMAT = Regex("[0-9a-f]{64}")

/** PostgreSQL that logs every statement, to prove the role's password never reaches its log. */
@TestConfiguration(proxyBeanMethods = false)
class StatementLoggingPostgres {
    @Bean
    @ServiceConnection
    fun postgres(): PostgreSQLContainer =
        PostgreSQLContainer(DockerImageName.parse("postgres:18-alpine"))
            .withCommand("postgres", "-c", "log_statement=all")
}

/**
 * The mechanism switched on (SARD_SELF_DIR set): the whole context with the real database, the real token
 * store and the real role. Rules "Пароль роли sard_self ...", "Проверка ... " and "Регистрация по встроенному
 * токену ..." of docs/specs/server/self-agent.feature.
 */
@MutFlowTest
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.self-agent.check-interval=1h"],
)
@Import(StatementLoggingPostgres::class, TokenServiceTestConfiguration::class, ConfirmedCaStep::class)
class SelfAgentWiringIntegrationTest(
    @Autowired private val check: SelfAgentCheck,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val enrollment: Enrollment,
    @Autowired private val ca: CertificateAuthority,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val postgres: PostgreSQLContainer,
    @Autowired private val dataSource: javax.sql.DataSource,
) {
    private val tokenFile = channel.resolve("enroll-token")
    private val passwordFile = channel.resolve("db-password")

    @BeforeTest
    fun `a known clock and no built-in tokens or agents`() {
        awaitFirstCheck()
        clean()
        clock.now = T0
    }

    @AfterTest
    fun `drop what the test enrolled`() = clean()

    /** The check at the start of the server runs on its own thread; the tests drive the later ones. */
    private fun awaitFirstCheck() {
        if (!firstCheckSeen.compareAndSet(false, true)) return
        val deadline = System.nanoTime() + Duration.ofSeconds(10).toNanos()
        while (!Files.exists(tokenFile)) {
            check(System.nanoTime() < deadline) { "no check ran at the start of the server" }
            Thread.sleep(20)
        }
    }

    private fun runCheck() = MutFlow.underTest { check.run() }

    private fun clean() {
        val tenant = DEFAULT_TENANT
        jdbc.update("delete from agent_certificates where agent_id in (select id from agents where builtin)")
        jdbc.update("delete from enrollment_tokens where builtin and tenant_id = ?", tenant)
        jdbc.update("delete from agents where builtin and tenant_id = ?", tenant)
    }

    private fun count(sql: String) = jdbc.queryForObject(sql, Int::class.java)

    private fun builtinTokens() = count("select count(*) from enrollment_tokens where builtin")

    private fun tokenOnDisk() = Files.readString(tokenFile)

    private fun loginAs(password: String) = DriverManager.getConnection(postgres.jdbcUrl, "sard_self", password).use { }

    @Test
    fun `Сервер при старте пишет пароль, а роль входит с ним и хранит проверочные данные SCRAM`() {
        val password = Files.readString(passwordFile)

        assertTrue(FORMAT.matches(password), password)
        assertEquals("rw-------", PosixFilePermissions.toString(Files.getPosixFilePermissions(passwordFile)))
        loginAs(password)
        assertFailsWith<SQLException> { loginAs("0".repeat(64)) }
        val sql = "select rolpassword from pg_authid where rolname = 'sard_self'"
        val verifier = jdbc.queryForObject(sql, String::class.java)
        assertTrue(verifier.orEmpty().startsWith("SCRAM-SHA-256$"))
    }

    @Test
    fun `Роль, которой нет, даёт ошибку SQL, по которой сервер предупреждает`() {
        val roles = ScramRolePasswords(dataSource)
        val failure = assertFailsWith<SQLException> { MutFlow.underTest { roles.set("no_such_role", "0".repeat(64)) } }

        assertEquals("42704", failure.sqlState)
    }

    @Test
    fun `Пароль роли не попадает в журнал PostgreSQL`() {
        assertFalse(Files.readString(passwordFile) in postgres.logs)
    }

    @Test
    fun `В канале нет файлов, кроме db-password и enroll-token`() {
        runCheck()

        val names = Files.list(channel).use { files -> files.map { it.fileName.toString() }.sorted().toList() }
        assertEquals(listOf("db-password", "enroll-token"), names)
    }

    @Test
    fun `Проверка выпускает встроенный токен тенанта по умолчанию на час, а файл содержит его строку`() {
        runCheck()

        assertEquals(1, builtinTokens())
        val expiresAt =
            jdbc.queryForObject(
                "select expires_at from enrollment_tokens where builtin",
                java.sql.Timestamp::class.java,
            )
        assertEquals(T0 + Duration.ofHours(1), expiresAt?.toInstant())
        val token = EnrollmentToken.parse(tokenOnDisk())
        assertEquals(ca.fingerprint(), token.fingerprint)
        assertTrue(tokens.builtinUsable(DEFAULT_TENANT, token.secret.hash(), Duration.ZERO))
        assertEquals("rw-------", PosixFilePermissions.toString(Files.getPosixFilePermissions(tokenFile)))
        val sql = "select t::text from enrollment_tokens t where builtin"
        val stored = jdbc.queryForObject(sql, String::class.java).orEmpty()
        assertFalse(tokenOnDisk() in stored)
    }

    @Test
    fun `Токен за миллисекунду до запаса не заменяется, на запасе заменяется`() {
        runCheck()
        val first = tokenOnDisk()

        clock.now = T0 + Duration.ofMinutes(50) - Duration.ofMillis(1)
        runCheck()
        assertEquals(first, tokenOnDisk())
        assertEquals(1, builtinTokens())

        clock.now = T0 + Duration.ofMinutes(50)
        runCheck()
        assertTrue(tokenOnDisk() != first)
        assertEquals(2, builtinTokens())
        assertEquals(1, count("select count(*) from enrollment_tokens where builtin and revoked_at is null"))
    }

    @Test
    fun `Агент, зарегистрированный по токену из файла, встроенный, и после этого файл удаляется`() {
        runCheck()

        enrollment.enroll(tokenOnDisk(), resource("agent-p256.csr"), "sard-self")
        runCheck()

        assertEquals(1, count("select count(*) from agents where builtin and revoked_at is null"))
        assertFalse(Files.exists(tokenFile))
        assertEquals(1, builtinTokens())
    }

    @Test
    fun `После отзыва встроенного агента проверка выпускает новый токен`() {
        runCheck()
        enrollment.enroll(tokenOnDisk(), resource("agent-p256.csr"), "sard-self")
        runCheck()
        jdbc.update("update agents set revoked_at = ? where builtin", java.sql.Timestamp.from(T0))

        runCheck()

        assertNotNull(EnrollmentToken.parse(tokenOnDisk()))
        assertEquals(2, builtinTokens())
    }

    @Test
    fun `Файл с токеном, строка которого нарушает формат, заменяется`() {
        runCheck()
        val active = tokenOnDisk()

        for (broken in listOf(active.dropLast(1) + "G", active.dropLast(1), "sard_" + active.drop(6))) {
            Files.writeString(tokenFile, broken)

            runCheck()

            assertTrue(tokenOnDisk() != broken, broken)
            assertTrue(tokens.builtinUsable(DEFAULT_TENANT, EnrollmentToken.parse(tokenOnDisk()).secret.hash(), MARGIN))
        }
    }

    @Test
    fun `Встроенные токены и агенты другого тенанта не влияют на проверку`() {
        val other = UUID.randomUUID()
        jdbc.insertTenant(other)
        try {
            enrollment.enroll(tokens.replaceBuiltin(other).reveal(), resource("agent-p256.csr"), "sard-self")
            val foreign = tokens.replaceBuiltin(other).reveal()
            Files.writeString(tokenFile, foreign)

            runCheck()

            assertTrue(tokenOnDisk() != foreign)
            assertTrue(tokens.builtinUsable(DEFAULT_TENANT, EnrollmentToken.parse(tokenOnDisk()).secret.hash(), MARGIN))
        } finally {
            jdbc.deleteEnrollmentTenantData(other)
        }
    }

    companion object {
        private val channel: Path = Files.createTempDirectory("sard-self-channel")
        private val firstCheckSeen =
            java.util.concurrent.atomic
                .AtomicBoolean(false)

        @JvmStatic
        @DynamicPropertySource
        fun properties(registry: DynamicPropertyRegistry) {
            registry.add("sard.self-agent.dir") { channel.toString() }
        }
    }
}
