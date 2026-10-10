// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import ch.qos.logback.classic.Level.WARN
import dev.sard.server.api.SETUP_COOKIE
import dev.sard.server.pki.CaFingerprint
import dev.sard.server.pki.CaImportFixtures
import dev.sard.server.pki.CaImportRefusal
import dev.sard.server.pki.CaImportRefusal.IMPORT_SOURCE_MISSING
import dev.sard.server.pki.CaImportRefused
import dev.sard.server.pki.CaStartRefusal
import dev.sard.server.pki.CaStartRefused
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.Pem
import org.junit.jupiter.api.io.TempDir
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.jdbc.datasource.DriverManagerDataSource
import java.nio.file.Files
import java.nio.file.Path
import java.security.cert.X509Certificate
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.TrustManager
import javax.net.ssl.X509TrustManager
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val TENANT = UUID.fromString("00000000-0000-0000-0000-000000000001")
private val TENANT_B = UUID.fromString("00000000-0000-0000-0000-0000000000b5")
private const val CODE_LINE = "SARD SETUP CODE"
private const val REVOKED_TOKENS = "select count(*) from enrollment_tokens where revoked_at is not null"
private const val NO_DATABASE = "jdbc:postgresql://localhost:1/none"
private const val INSERT_ORIGIN =
    "insert into ca_origins (fingerprint, origin, recorded_at) values (?, 'generated', now())"

/** The names of what is in [directory], none if there is no such directory. */
private fun names(directory: Path): List<String> {
    if (Files.notExists(directory)) return emptyList()
    return Files.list(directory).use { found -> found.map { it.fileName.toString() }.sorted().toList() }
}

private fun Throwable.messages(): String {
    val chain = generateSequence(this) { it.cause }
    return chain.mapNotNull { it.message }.joinToString("\n")
}

private inline fun <reified T : Throwable> Throwable.cause(): T? {
    val chain = generateSequence(this) { it.cause }
    return chain.filterIsInstance<T>().firstOrNull()
}

/**
 * The scenarios of docs/specs/server/onboarding-setup.feature about the CA at start: a generated CA replaced by an
 * import (Р11), a source that must be readable before the step ca (Р18), and the CA directory that goes with its
 * database (Р19). Real servers, started one after another over one installation.
 */
class CaStartupIntegrationTest {
    @TempDir
    lateinit var tmp: Path

    private val running = mutableListOf<RunningServer>()
    private val installation by lazy { Installation(tmp) }
    private val importDir get() = tmp.resolve("import")
    private val f = CaImportFixtures.original()
    private val fHex get() = CaFingerprint.of(f.certificate).hex

    @AfterTest
    fun `stop every server`() {
        running.forEach { runCatching { it.close() } }
    }

    private fun start(
        installation: Installation = this.installation,
        vararg properties: Pair<String, Any>,
        codes: List<String> = listOf(CODE),
    ): RunningServer {
        val server = installation.start(StartOptions(codes = codes, properties = mapOf(*properties)))
        running += server
        return server
    }

    private fun restart(
        server: RunningServer,
        vararg properties: Pair<String, Any>,
    ): RunningServer {
        server.close()
        running -= server
        return start(this.installation, *properties)
    }

    private fun fingerprintOf(server: RunningServer) = server.bean(CertificateAuthority::class.java).fingerprint().hex

    private fun withSource(): Array<Pair<String, Any>> = arrayOf("sard.pki.import-dir" to importDir.toString())

    private fun jdbc(installation: Installation = this.installation) =
        installation.database.let { JdbcTemplate(DriverManagerDataSource(it.url, it.user, it.password)) }

    private fun filesIn(path: Path): List<String> =
        Files.walk(path).use { s ->
            s
                .filter {
                    it != path
                }.map {
                    "$it ${Files.getLastModifiedTime(
                        it,
                    )} ${if (Files.isRegularFile(it)) Files.readString(it).hashCode() else ""}"
                }.sorted()
                .toList()
        }

    private fun agentCertificate(
        jdbc: JdbcTemplate,
        tenant: UUID = TENANT,
        builtin: Boolean = false,
        revoked: Boolean = false,
    ) {
        val agent = UUID.randomUUID()
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, registered_at, builtin) values (?, ?, 'h', now(), ?)",
            agent,
            tenant,
            builtin,
        )
        jdbc.update(
            "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after, revoked_at) " +
                "values (?, ?, ?, now(), now() + interval '1 day', ?)",
            "e".repeat(32),
            tenant,
            agent,
            if (revoked) Timestamp.from(Instant.now()) else null,
        )
    }

    /** The origin the database has recorded for the CA with this fingerprint, if any. */
    private fun originRecordedFor(
        fingerprint: String,
        of: Installation = installation,
    ): String? {
        val sql = "select origin from ca_origins where fingerprint = ?"
        return jdbc(of).queryForList(sql, String::class.java, fingerprint).firstOrNull()
    }

    private fun forgetCertificates(jdbc: JdbcTemplate) {
        jdbc.update("delete from agent_certificates")
        jdbc.update("delete from agents")
        jdbc.update("delete from tenants where id = ?", TENANT_B)
    }

    private fun sourceOf(pair: dev.sard.server.pki.CaKeyPair = f) = CaImportFixtures.source(importDir, pair)

    /** The CA of an installation as a source of an import (a backup of the CA directory). */
    private fun sourceOfDirectory(ca: Path) {
        val certificate = Files.readString(ca.resolve("ca.crt"))
        CaImportFixtures.source(importDir, certificate, Files.readString(ca.resolve("ca.key")))
    }

    // ---- Правило: Сгенерированный CA заменяется импортом, пока шаг ca не выполнен и сертификатов нет ----

    @Test
    fun `Импорт до шага ca заменяет сгенерированный CA`() {
        val first = start()
        val g = fingerprintOf(first)
        sourceOf()

        val second = restart(first, *withSource())

        assertEquals(fHex, fingerprintOf(second))
        val line = second.startLog.map { it.text }.single { "CA imported from" in it }
        for (part in listOf(importDir.toString(), "fingerprint=$fHex", "origin=imported", "replaced=$g")) {
            assertTrue(part in line, line)
        }
    }

    @Test
    fun `После замены в каталоге CA только ca с ca crt и ca key, а мастер показывает импортированный CA`() {
        val first = start()
        val oldKey = Files.readString(installation.pkiDir.resolve("ca/ca.key"))
        sourceOf()

        val second = restart(first, *withSource())

        assertEquals(listOf("ca"), names(installation.pkiDir))
        assertEquals(
            listOf("ca.crt", "ca.key"),
            Files.list(installation.pkiDir.resolve("ca")).use { s ->
                s.map { it.fileName.toString() }.sorted().toList()
            },
        )
        assertNotEquals(oldKey, Files.readString(installation.pkiDir.resolve("ca/ca.key")))
        assertEquals(Pem.privateKey(f.privateKey), Files.readString(installation.pkiDir.resolve("ca/ca.key")))
        val session = second.client.setupSession()
        val ca =
            second.client
                .state(session)
                .json
                .path("ca")
        assertEquals(fHex, ca.path("fingerprint").asString())
        assertEquals("imported", ca.path("origin").asString())
    }

    @Test
    fun `Происхождение импортированного CA сохраняется после перезапуска без источника`() {
        val first = start()
        sourceOf()
        val second = restart(first, *withSource())

        val third = restart(second)

        assertTrue(third.startLog.any { "fingerprint=$fHex" in it.text && "origin=existing" in it.text }, third.logText)
        val session = third.client.setupSession()
        assertEquals(
            "imported",
            third.client
                .state(session)
                .json
                .path("ca")
                .path("origin")
                .asString(),
        )
    }

    @Test
    fun `После замены сертификат gRPC-порта подписан новым CA`() {
        val first = start()
        val g = first.bean(CertificateAuthority::class.java).fingerprint()
        sourceOf()
        val second = restart(first, *withSource())

        val chain = serverChain(second.grpcPort)

        assertEquals(fHex, CaFingerprint.of(chain.last()).hex)
        chain.first().verify(f.certificate.publicKey)
        assertNotEquals(g.hex, CaFingerprint.of(chain.last()).hex)
    }

    private fun serverChain(port: Int): List<X509Certificate> {
        val trustAll =
            arrayOf<TrustManager>(
                object : X509TrustManager {
                    override fun checkClientTrusted(
                        chain: Array<X509Certificate>,
                        authType: String,
                    ) = Unit

                    override fun checkServerTrusted(
                        chain: Array<X509Certificate>,
                        authType: String,
                    ) = Unit

                    override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
                },
            )
        val context = SSLContext.getInstance("TLS").apply { init(null, trustAll, null) }
        context.socketFactory.createSocket("localhost", port).use { socket ->
            (socket as javax.net.ssl.SSLSocket).startHandshake()
            return socket.session.peerCertificates.map { it as X509Certificate }
        }
    }

    @Test
    fun `Замена CA отзывает активные токены, одна строка WARN с числом`() {
        val first = start()
        val jdbc = jdbc()
        jdbc.update("insert into tenants (id, name) values (?, 'B')", TENANT_B)
        for (tenant in listOf(TENANT, TENANT_B)) {
            jdbc.update(
                "insert into enrollment_tokens (id, tenant_id, token_hash, expires_at, created_at, label) " +
                    "values (?, ?, ?, now() + interval '1 day', now(), '')",
                UUID.randomUUID(),
                tenant,
                ByteArray(32) { (tenant.hashCode() + it).toByte() },
            )
        }
        sourceOf()

        val second = restart(first, *withSource())

        val revoked = jdbc.queryForObject(REVOKED_TOKENS, Int::class.java)
        assertEquals(2, revoked)
        val warnings = second.startLog.filter { it.level == WARN && "enrollment tokens revoked" in it.text }
        assertEquals(1, warnings.size, second.logText)
        assertTrue(" 2 " in warnings.single().text, warnings.single().text)
        jdbc.update("delete from enrollment_tokens")
        jdbc.update("delete from tenants where id = ?", TENANT_B)
    }

    @Test
    fun `Тот же CA в источнике до шага ca не меняет каталог`() {
        val first = start()
        val g = fingerprintOf(first)
        val before = filesIn(installation.pkiDir)
        first.close()
        running -= first
        val restored = installation.pkiDir.resolve("ca")
        sourceOfDirectory(restored)

        val second = start(installation, *withSource())

        assertEquals(g, fingerprintOf(second))
        assertEquals(before, filesIn(installation.pkiDir))
        assertTrue(
            second.startLog.any { it.level == ch.qos.logback.classic.Level.INFO && "CA import not needed" in it.text },
            second.logText,
        )
    }

    @Test
    fun `После шага ca импорт другого CA отклоняется CA_ALREADY_PRESENT с причиной`() {
        val first = start()
        val g = fingerprintOf(first)
        first.client.completeWizard()
        first.close()
        running -= first
        sourceOf()
        val before = filesIn(installation.pkiDir)

        val (failure, _) = installation.startFailing(StartOptions(properties = mapOf(*withSource())))

        val refused = checkNotNull(failure.cause<CaImportRefused>())
        assertEquals(CaImportRefusal.CA_ALREADY_PRESENT, refused.reason)
        val message = refused.message.orEmpty()
        assertTrue(g in message && fHex in message, message)
        assertTrue("onboarding step ca is complete" in message, message)
        assertTrue("docs/operator/08-migrate-and-remove.md" in message, message)
        assertEquals(before, filesIn(installation.pkiDir))
    }

    @Test
    fun `Выданный сертификат агента запрещает замену CA`() {
        val first = start()
        val g = fingerprintOf(first)
        first.close()
        running -= first
        sourceOf()
        val jdbc = jdbc()
        val before = filesIn(installation.pkiDir)
        val variants: List<(JdbcTemplate) -> Unit> =
            listOf(
                { agentCertificate(it) },
                { agentCertificate(it, builtin = true) },
                { agentCertificate(it, revoked = true) },
                {
                    it.update("insert into tenants (id, name) values (?, 'B')", TENANT_B)
                    agentCertificate(it, tenant = TENANT_B)
                },
            )
        for (variant in variants) {
            variant(jdbc)

            val (failure, _) = installation.startFailing(StartOptions(properties = mapOf(*withSource())))

            val refused = checkNotNull(failure.cause<CaImportRefused>()) { failure.toString() }
            assertEquals(CaImportRefusal.CA_ALREADY_PRESENT, refused.reason)
            assertTrue(
                g in refused.message.orEmpty() && fHex in refused.message.orEmpty() &&
                    "agent certificates issued" in refused.message.orEmpty(),
            )
            assertEquals(before, filesIn(installation.pkiDir))
            forgetCertificates(jdbc)
        }
    }

    // ---- Правило: До шага ca нечитаемый или неподходящий источник не даёт серверу стартовать ----

    @Test
    fun `До шага ca недоступный источник даёт отказ старта, и каталог CA не меняется`() {
        val first = start()
        first.close()
        running -= first
        val before = filesIn(installation.pkiDir)
        val cases =
            listOf(
                Triple("путь источника не существует", { Files.deleteIfExists(importDir) }, IMPORT_SOURCE_MISSING),
                Triple("нет файла сертификата", {
                    sourceOf()
                    Files.delete(importDir.resolve("ca/ca.crt"))
                    true
                }, CaImportRefusal.IMPORT_FILE_MISSING),
            )
        for ((name, prepare, reason) in cases) {
            importDir.toFile().deleteRecursively()
            prepare()

            val (failure, _) = installation.startFailing(StartOptions(properties = mapOf(*withSource())))

            val refused = checkNotNull(failure.cause<CaImportRefused>()) { name }
            assertEquals(reason, refused.reason, name)
            assertTrue("SARD_PKI_IMPORT_DIR" in refused.message.orEmpty(), name)
            assertEquals(before, filesIn(installation.pkiDir), name)
        }
    }

    @Test
    fun `До шага ca источник отвергнут и при выданных сертификатах, и с тем же CA при слишком открытых правах`() {
        val first = start()
        first.close()
        running -= first
        val jdbc = jdbc()
        val before = filesIn(installation.pkiDir)
        agentCertificate(jdbc)

        val (missing, _) =
            installation.startFailing(
                StartOptions(
                    properties =
                        mapOf("sard.pki.import-dir" to tmp.resolve("nowhere").toString()),
                ),
            )
        assertEquals(CaImportRefusal.IMPORT_SOURCE_MISSING, checkNotNull(missing.cause<CaImportRefused>()).reason)
        forgetCertificates(jdbc)

        val restored = installation.pkiDir.resolve("ca")
        sourceOfDirectory(restored)
        CaImportFixtures.chmod(importDir.resolve("ca/ca.key"), "rw-r-----")
        val (open, _) = installation.startFailing(StartOptions(properties = mapOf(*withSource())))
        assertEquals(CaImportRefusal.IMPORT_PERMISSIONS_TOO_OPEN, checkNotNull(open.cause<CaImportRefused>()).reason)
        assertEquals(before, filesIn(installation.pkiDir))
    }

    @Test
    fun `После шага ca недоступный источник только предупреждает`() {
        val first = start()
        val g = fingerprintOf(first)
        first.client.completeWizard()
        val before = filesIn(installation.pkiDir)

        val second = restart(first, "sard.pki.import-dir" to tmp.resolve("nowhere").toString())

        assertEquals(g, fingerprintOf(second))
        val warning = second.startLog.single { it.level == WARN && "CA import skipped" in it.text }
        assertTrue("SARD_PKI_IMPORT_DIR" in warning.text, warning.text)
        assertEquals(before, filesIn(installation.pkiDir))
    }

    // ---- Правило: Каталог CA и база ставятся вместе, CA без записанного происхождения не даёт серверу стартовать ----

    private fun refusal(
        installation: Installation,
        vararg properties: Pair<String, Any>,
    ): Pair<CaStartRefused, String> {
        val (failure, log) = installation.startFailing(StartOptions(properties = mapOf(*properties)))
        val refused = checkNotNull(failure.cause<CaStartRefused>()) { failure.toString() }
        return refused to log.joinToString("\n") { it.text }
    }

    @Test
    fun `CA в каталоге без записанного происхождения даёт отказ старта`() {
        val first = start()
        val g = fingerprintOf(first)
        first.close()
        running -= first
        val reinstalledDatabase = Installation(tmp, TestPostgres.newDatabase())
        val before = filesIn(installation.pkiDir)
        val other = CaFingerprint("c".repeat(64)).hex
        val prepared: List<(JdbcTemplate) -> Unit> =
            listOf(
                { },
                {
                    it.update(INSERT_ORIGIN, other)
                    it.update("insert into onboarding_steps (step, completed_at) values ('ca', now())")
                },
                { it.update("delete from onboarding_steps") },
            )
        for (prepare in prepared) {
            prepare(jdbc(reinstalledDatabase))

            val (refused, log) = refusal(reinstalledDatabase)

            assertEquals(CaStartRefusal.CA_ORIGIN_NOT_RECORDED, refused.reason)
            val message = refused.message.orEmpty()
            assertTrue(message.startsWith("CA startup refused"), message)
            assertTrue(g in message && installation.pkiDir.resolve("ca").toString() in message, message)
            assertTrue("CA origin is not recorded in the database" in message, message)
            assertTrue("server volumes are reinstalled together" in message, message)
            assertTrue("docs/operator/09-troubleshooting.md" in message, message)
            assertEquals(before, filesIn(installation.pkiDir))
            assertFalse(CODE_LINE in log, "a code was printed")
            assertNull(originRecordedFor(g, reinstalledDatabase))
        }
    }

    @Test
    fun `Отказ без записанного происхождения не зависит от источника импорта`() {
        val first = start()
        first.close()
        running -= first
        val reinstalledDatabase = Installation(tmp, TestPostgres.newDatabase())
        val restored = installation.pkiDir.resolve("ca")
        val before = filesIn(installation.pkiDir)
        val sources: List<() -> Array<Pair<String, Any>>> =
            listOf(
                { emptyArray<Pair<String, Any>>() },
                {
                    CaImportFixtures.source(
                        importDir,
                        Files.readString(restored.resolve("ca.crt")),
                        Files.readString(restored.resolve("ca.key")),
                    )
                    withSource()
                },
                {
                    importDir.toFile().deleteRecursively()
                    sourceOf()
                    withSource()
                },
                {
                    importDir.toFile().deleteRecursively()
                    arrayOf<Pair<String, Any>>("sard.pki.import-dir" to tmp.resolve("nowhere").toString())
                },
            )
        for (source in sources) {
            val (refused, _) = refusal(reinstalledDatabase, *source())

            assertEquals(CaStartRefusal.CA_ORIGIN_NOT_RECORDED, refused.reason)
            assertEquals(before, filesIn(installation.pkiDir))
        }
    }

    @Test
    fun `Пустой каталог CA без источника при базе, где CA в деле, даёт отказ старта`() {
        val first = start()
        first.client.completeWizard()
        first.close()
        running -= first
        val lostCa = Installation(tmp.resolve("lost").also { Files.createDirectories(it) }, installation.database)
        val jdbc = jdbc()
        val cases =
            listOf(
                "onboarding step ca is complete" to { },
                "agent certificates issued" to {
                    jdbc.update("delete from onboarding_steps")
                    agentCertificate(jdbc)
                },
                "agent certificates issued" to {
                    jdbc.update("delete from agent_certificates")
                    agentCertificate(jdbc, revoked = true)
                },
            )
        for ((reason, prepare) in cases) {
            prepare()

            val (refused, _) = refusal(lostCa)

            assertEquals(CaStartRefusal.CA_MISSING, refused.reason, reason)
            val message = refused.message.orEmpty()
            assertTrue(lostCa.pkiDir.resolve("ca").toString() in message && "CA directory is empty" in message, message)
            assertTrue(reason in message && "SARD_PKI_IMPORT_DIR" in message, message)
            assertTrue("server volumes are reinstalled together" in message, message)
            assertTrue("docs/operator/09-troubleshooting.md" in message, message)
            assertFalse(Files.exists(lostCa.pkiDir.resolve("ca")), "a CA was made")
        }
        forgetCertificates(jdbc)
    }

    @Test
    fun `Пустой каталог CA с источником при восстановленной базе импортирует CA`() {
        val first = start()
        first.client.completeWizard()
        first.close()
        running -= first
        val moved = Installation(tmp.resolve("moved").also { Files.createDirectories(it) }, installation.database)
        val restored = installation.pkiDir.resolve("ca")
        sourceOfDirectory(restored)
        val g = CaFingerprint.of(PkiFixturesCertificate.of(Files.readString(restored.resolve("ca.crt")))).hex

        val server = start(moved, *withSource())

        assertEquals(g, fingerprintOf(server))
        assertEquals("imported", originRecordedFor(g))
    }

    @Test
    fun `Пустой каталог CA при базе, где CA не в деле, получает новый CA`() {
        val first = start()
        val g = fingerprintOf(first)
        first.close()
        running -= first
        val lostCa = Installation(tmp.resolve("lost").also { Files.createDirectories(it) }, installation.database)

        val second = start(lostCa)

        assertNotEquals(g, fingerprintOf(second))
        assertEquals("generated", originRecordedFor(fingerprintOf(second)))
    }

    @Test
    fun `Недоступная база при первом старте не создаёт CA`() {
        val nowhere =
            Installation(tmp.resolve("nodb").also { Files.createDirectories(it) }, Database(NO_DATABASE, "u", "p"))

        val (failure, _) = nowhere.startFailing()

        assertTrue(failure.messages().isNotBlank())
        assertFalse(Files.exists(nowhere.pkiDir.resolve("ca")))
    }

    @Test
    fun `Сбой записи происхождения при первом старте оставляет каталог CA пустым`() {
        val jdbc = jdbc()
        start().close()
        running.clear()
        installation.pkiDir.toFile().deleteRecursively()
        jdbc.update("delete from ca_origins")
        jdbc.execute(
            "create function refuse_origin() returns trigger as \$\$ " +
                "begin raise exception 'no origins'; end \$\$ language plpgsql",
        )
        jdbc.execute(
            "create trigger refuse_origin before insert on ca_origins " +
                "for each row execute function refuse_origin()",
        )

        val (failure, _) = installation.startFailing()

        assertTrue(failure.messages().isNotBlank())
        assertFalse(Files.exists(installation.pkiDir.resolve("ca")))
        assertEquals(
            emptyList(),
            names(installation.pkiDir),
        )
        jdbc.execute("drop trigger refuse_origin on ca_origins")
        jdbc.execute("drop function refuse_origin()")
    }

    @Test
    fun `Одновременные первые старты с одной базой получают один CA с записанным происхождением`() {
        // The database is migrated and its partitions made before: the servers race for the CA, not for the schema.
        start().close()
        running.clear()
        installation.pkiDir
            .resolve("ca")
            .toFile()
            .deleteRecursively()
        jdbc().update("delete from ca_origins")
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)
        val servers =
            (1..2).map {
                pool.submit<RunningServer> {
                    start.await()
                    installation.start(StartOptions(codes = listOf(CODE)))
                }
            }
        start.countDown()
        val both = servers.map { it.get(180, TimeUnit.SECONDS) }
        running += both
        pool.shutdown()

        assertEquals(1, both.map { fingerprintOf(it) }.toSet().size)
        assertEquals("generated", originRecordedFor(fingerprintOf(both.first())))
    }

    // ---- Правило: Встроенный токен sard-self не выпускается до шага ca ----

    @Test
    fun `Чистая установка до шага ca не выпускает встроенный токен, после подтверждения CA — выпускает`() {
        val server =
            start(
                installation,
                "sard.self-agent.dir" to installation.selfDir.toString(),
                "sard.self-agent.check-interval" to "100ms",
            )

        Thread.sleep(600)
        assertEquals(0, jdbc().queryForObject("select count(*) from enrollment_tokens where builtin", Int::class.java))
        assertFalse(Files.exists(installation.selfDir.resolve("enroll-token")))
        assertTrue(Files.exists(installation.selfDir.resolve("db-password")), "the role password is not set")
        val password = Files.readString(installation.selfDir.resolve("db-password"))
        java.sql.DriverManager
            .getConnection(installation.database.url, "sard_self", password)
            .use { assertTrue(it.isValid(5)) }

        val session = server.client.setupSession()
        assertEquals(204, server.client.confirmCa(session).status)

        val deadline = System.currentTimeMillis() + 10_000
        val tokenFile = installation.selfDir.resolve("enroll-token")
        while (!Files.exists(tokenFile) && System.currentTimeMillis() < deadline) Thread.sleep(100)
        val token = Files.readString(installation.selfDir.resolve("enroll-token"))
        assertEquals(1, jdbc().queryForObject("select count(*) from enrollment_tokens where builtin", Int::class.java))
        assertTrue(token.endsWith("." + fingerprintOf(server)), "the token carries the fingerprint of the CA")
        assertNotEquals("", session)
        assertTrue(SETUP_COOKIE.isNotEmpty())
    }
}

/** The certificate in a PEM text. */
private object PkiFixturesCertificate {
    fun of(pem: String): X509Certificate =
        dev.sard.server.pki.PkiFixtures
            .certificate(pem)
}
