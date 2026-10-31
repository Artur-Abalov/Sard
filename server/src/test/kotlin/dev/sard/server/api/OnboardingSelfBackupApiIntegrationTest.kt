// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.extension.TenantResolver
import dev.sard.server.onboarding.OnboardingSteps
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RepositoryEntry
import dev.sard.server.selfagent.captureEvents
import org.mockito.Mockito
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.dao.DataAccessResourceFailureException
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean
import tools.jackson.databind.ObjectMapper
import java.time.Duration
import java.util.UUID
import java.util.concurrent.Executors
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val DEFAULT_TENANT = TenantResolver.DEFAULT_TENANT_ID
private const val KEYS = "/api/v1/self-backup/keys-confirmation"
private const val CONFIRMED = """{"copiedOutsideServer":true}"""
private const val EVIL = "https://evil.example"
private val HOUR: Duration = Duration.ofHours(1)

/**
 * The self-backup steps and the key copy of docs/specs/server/onboarding-self-backup.feature (@http): the state of the
 * wizard, the confirmation, the command and the card, over REST with the default tenant, where the wizard lives.
 */
@RestApiTest
class OnboardingSelfBackupApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
) {
    @MockitoSpyBean
    lateinit var steps: OnboardingSteps

    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private var admin = world.admin(DEFAULT_TENANT)

    @BeforeTest
    fun `the wizard of F4a is done, the onboarding is not complete`() {
        world.wipe(DEFAULT_TENANT)
        jdbc.startOnboarding()
    }

    @AfterTest
    fun `the onboarding is complete again, forget the default tenant`() {
        Mockito.reset(steps)
        jdbc.finishOnboarding()
        world.wipe(DEFAULT_TENANT)
        world.close()
    }

    private fun send(
        method: String,
        path: String,
        body: String? = null,
        session: ApiSession? = admin,
        headers: Map<String, String> = emptyMap(),
    ) = world.api.send(method, path, session, body, headers)

    private fun bind(
        repository: String = "self",
        extra: String = "",
        confirm: Boolean = repository == "disk",
    ): ApiResponse {
        val local = if (confirm) ""","confirmLocalStorage":true""" else ""
        return send("PUT", "/api/v1/self-backup/repository", """{"repositoryName":"$repository"$extra$local}""")
    }

    private fun backUp(status: String = "succeeded") {
        val runs = send("POST", "/api/v1/self-backup/runs", "").json.path("runs").list()
        for (run in runs) world.forceRun(UUID.fromString(run.path("runId").asString()), status)
    }

    private fun onboarding() = send("GET", "/api/v1/onboarding").json

    private fun stepStates() = onboarding().path("steps").list().map { it.path("state").asString() }

    private fun restriction() = onboarding().path("restriction").asString()

    private fun selfBackup() = send("GET", "/api/v1/self-backup").json

    private fun readyToConfirm() {
        world.selfAgent(DEFAULT_TENANT)
        assertEquals(200, bind().status)
        backUp()
    }

    private fun confirm(
        body: String? = CONFIRMED,
        session: ApiSession? = admin,
        headers: Map<String, String> = emptyMap(),
    ) = send("POST", KEYS, body, session, headers)

    // ---- Правило: Шаги самобэкапа и копии ключей идут после шага admin и друг за другом ----

    @Test
    fun `После шага admin шаг самобэкапа ожидает выполнения`() {
        assertEquals(listOf("done", "done", "pending", "upcoming"), stepStates())
        assertEquals("full", restriction())
    }

    @Test
    fun `Привязка репозитория без успешных бэкапов не выполняет шаг самобэкапа`() {
        world.selfAgent(DEFAULT_TENANT)

        bind()

        assertEquals(listOf("done", "done", "pending", "upcoming"), stepStates())
        assertFalse(selfBackup().path("firstBackupDone").asBoolean())
    }

    @Test
    fun `Шаг самобэкапа выполнен, когда оба системных источника успешно забэкапились после привязки`() {
        world.selfAgent(DEFAULT_TENANT)
        bind()

        backUp()

        assertEquals(listOf("done", "done", "done", "pending"), stepStates())
        assertTrue(selfBackup().path("firstBackupDone").asBoolean())
        assertEquals("full", restriction())
    }

    @Test
    fun `Успех только одного системного источника не выполняет шаг самобэкапа`() {
        world.selfAgent(DEFAULT_TENANT)
        bind()
        val runs = send("POST", "/api/v1/self-backup/runs", "").json.path("runs").list()

        world.forceRun(UUID.fromString(runs[0].path("runId").asString()), "succeeded")
        world.forceRun(UUID.fromString(runs[1].path("runId").asString()), "failed", "boom")

        assertEquals("pending", stepStates()[2])
        assertFalse(selfBackup().path("firstBackupDone").asBoolean())
    }

    @Test
    fun `Успешный запуск до последней привязки не засчитывается`() {
        world.selfAgent(DEFAULT_TENANT)
        bind()
        backUp()
        clock.now = T0 + HOUR
        admin = world.admin(DEFAULT_TENANT)

        bind("self-2")

        assertEquals("pending", stepStates()[2])
        assertFalse(selfBackup().path("firstBackupDone").asBoolean())
    }

    @Test
    fun `Повторная привязка того же репозитория не сбрасывает шаг самобэкапа`() {
        world.selfAgent(DEFAULT_TENANT)
        bind()
        backUp()
        clock.now = T0 + HOUR
        admin = world.admin(DEFAULT_TENANT)

        bind()

        assertEquals("done", stepStates()[2])
    }

    @Test
    fun `Без встроенного агента шаг самобэкапа не выполняется`() {
        val refused = bind()

        assertEquals(422, refused.status)
        assertEquals("self_agent_missing", refused.code)
        assertEquals("pending", stepStates()[2])
        assertEquals("full", restriction())
    }

    // ---- Правило: Подтверждение копии ключей завершает онбординг только после первых бэкапов ----

    @Test
    fun `Подтверждение копии ключей после первых бэкапов завершает онбординг`() {
        readyToConfirm()

        val response = confirm()

        assertEquals(204, response.status, response.toString())
        assertEquals(listOf("done", "done", "done", "done"), stepStates())
        assertEquals("none", restriction())
        assertEquals(T0.toString(), selfBackup().path("keysConfirmedAt").asString())
    }

    @Test
    fun `Подтверждение копии ключей до успешных первых бэкапов отклоняется`() {
        world.selfAgent(DEFAULT_TENANT)
        bind()

        val response = confirm()

        assertEquals(409, response.status)
        assertEquals("first_backup_pending", response.code)
        assertEquals("upcoming", stepStates()[3])
        assertEquals("full", restriction())
    }

    @Test
    fun `Подтверждение копии ключей без привязки отклоняется`() {
        val response = confirm()

        assertEquals(409, response.status)
        assertEquals("self_backup_not_configured", response.code)
        assertEquals("full", restriction())
    }

    @Test
    fun `Подтверждение без явной отметки отклоняется`() {
        readyToConfirm()
        val bodies =
            listOf(
                "{}",
                """{"copiedOutsideServer": false}""",
                """{"copiedOutsideServer": null}""",
                """{"copiedOutsideServer": "true"}""",
            )
        for (body in bodies) {
            val response = confirm(body)

            assertEquals(422, response.status, "$body: $response")
            assertEquals("validation_failed", response.code, body)
            assertTrue("copiedOutsideServer" in response.errorFields(), body)
        }
        assertEquals("full", restriction())
    }

    @Test
    fun `Подтверждение с телом не в JSON отклоняется`() {
        readyToConfirm()

        val response = confirm("yes")

        assertEquals(422, response.status, response.toString())
        assertEquals("validation_failed", response.code)
        assertEquals("full", restriction())
    }

    @Test
    fun `Подтверждение копии ключей требует сессии администратора`() {
        readyToConfirm()
        val forged = ApiSession("forged-session", DEFAULT_TENANT)

        for (session in listOf(null, forged)) {
            val response = confirm(session = session)

            assertEquals(401, response.status)
            assertEquals("unauthenticated", response.code)
        }
        assertEquals("full", restriction())
    }

    @Test
    fun `Подтверждение копии ключей с чужим Origin отклоняется`() {
        readyToConfirm()

        val response = confirm(headers = mapOf("Origin" to EVIL))

        assertEquals(403, response.status)
        assertEquals("origin_rejected", response.code)
        assertEquals("full", restriction())
    }

    @Test
    fun `Повторное подтверждение копии ключей ничего не меняет`() {
        readyToConfirm()
        assertEquals(204, confirm().status)
        clock.now = T0 + HOUR
        admin = world.admin(DEFAULT_TENANT)

        val lines = captureEvents { assertEquals(204, confirm().status) }

        assertEquals(T0.toString(), selfBackup().path("keysConfirmedAt").asString())
        assertEquals(0, lines.count { it.text == "Onboarding completed" })
    }

    @Test
    fun `Одновременные подтверждения из двух сессий завершают онбординг один раз`() {
        readyToConfirm()
        val second = world.api.signIn(DEFAULT_TENANT)
        val pool = Executors.newFixedThreadPool(2)

        val lines =
            captureEvents {
                val results = listOf(admin, second).map { pool.submit<ApiResponse> { confirm(session = it) } }
                assertEquals(listOf(204, 204), results.map { it.get().status })
            }
        pool.shutdown()

        assertEquals(1, lines.count { it.text == "Onboarding completed" })
    }

    @Test
    fun `Подтверждение при недоступной базе не завершает онбординг`() {
        readyToConfirm()
        Mockito.doThrow(DataAccessResourceFailureException("down")).`when`(steps).confirmKeys(T0)

        val response = confirm()

        assertEquals(503, response.status, response.toString())
        assertEquals("unavailable", response.code)
        Mockito.reset(steps)
        assertEquals("full", restriction())
    }

    @Test
    fun `Подтверждение копии ключей пишет в журнал адрес клиента и репозиторий`() {
        readyToConfirm()
        val repositoryId = selfBackup().path("repository").path("repositoryId").asString()

        val lines = captureEvents { confirm() }

        val step = lines.single { it.text.startsWith("Onboarding step keys_confirmed completed from ") }
        assertTrue(step.text.endsWith(": repository=self repository_id=$repositoryId"), step.text)
        assertEquals(1, lines.count { it.text == "Onboarding completed" })
    }

    // ---- Правило: Завершённый онбординг не возвращается ----

    @Test
    fun `После завершения онбординга сбой самобэкапа не возвращает ограничение`() {
        readyToConfirm()
        confirm()

        backUp("failed")

        assertEquals(listOf("done", "done", "done", "done"), stepStates())
        assertEquals("none", restriction())
        assertEquals(201, send("POST", "/api/v1/enrollment-tokens", "{}").status)
    }

    // ---- Правило: Мастер видит репозитории соседа и состав самобэкапа ----

    @Test
    fun `Состояние самобэкапа перечисляет репозитории последнего Register встроенного агента`() {
        val agent = world.selfAgent(DEFAULT_TENANT, listOf(RepositoryEntry("self", "s3", SELF_REPOSITORY_ID, "")))
        world.register(
            agent,
            snapshotOf(
                repositories =
                    listOf(
                        RepositoryEntry("self", "s3", SELF_REPOSITORY_ID, ""),
                        RepositoryEntry("self-2", "local", "", ""),
                    ),
            ),
        )

        val state = selfBackup()

        assertEquals(
            listOf("self" to false, "self-2" to true),
            state.path("repositories").list().map { it.path("name").asString() to it.path("local").asBoolean() },
        )
        assertEquals(
            SELF_REPOSITORY_ID,
            state
                .path("repositories")
                .path(0)
                .path("repositoryId")
                .asString(),
        )
        assertTrue(
            state
                .path("repositories")
                .path(1)
                .path("repositoryId")
                .isNull,
        )
        assertFalse(state.path("configured").asBoolean())
    }

    @Test
    fun `Без встроенного агента список репозиториев пуст`() {
        val state = selfBackup()

        assertTrue(state.path("agentId").isNull)
        assertEquals(0, state.path("repositories").size())
    }

    @Test
    fun `Состав самобэкапа виден до привязки и не содержит секретов`() {
        val response = send("GET", "/api/v1/self-backup")

        val plan = response.json.path("plan").list()
        assertEquals(listOf("self_database", "self_keys"), plan.map { it.path("role").asString() })
        assertEquals(listOf("postgresql", "files"), plan.map { it.path("plugin").asString() })
        assertTrue(
            plan[0]
                .path("includes")
                .path(0)
                .asString()
                .contains(" на "),
        )
        val files = plan[1].path("includes").list().map { it.asString() }
        assertEquals(listOf("/var/lib/sard/pki", "/etc/sard/install"), files)
        assertFalse("sard-db" in response.body || "password_ref" in response.body || "\"password\"" in response.body)
    }

    @Test
    fun `Команда показа пароля появляется после привязки, до неё её нет`() {
        world.selfAgent(DEFAULT_TENANT)
        assertTrue(selfBackup().path("passwordRevealCommand").isNull)
        assertNull(selfBackup().path("keysConfirmedAt").takeUnless { it.isNull })

        bind()

        assertEquals(
            "docker compose exec self-agent sard-agent repo password self --reveal --config /etc/sard/self/agent.yaml",
            selfBackup().path("passwordRevealCommand").asString(),
        )
    }

    @Test
    fun `Команда ключа восстановления появляется после привязки, до неё её нет`() {
        world.selfAgent(DEFAULT_TENANT)
        assertTrue(selfBackup().path("recoveryKeyCommand").isNull)

        bind()

        assertEquals(
            "docker compose run --rm --no-deps -u 0 --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER " +
                "--cap-add SETUID --cap-add SETGID self-agent repo key add self --for recovery --if-missing " +
                "--no-restart --config /etc/sard/self/agent.yaml && docker compose restart self-agent",
            selfBackup().path("recoveryKeyCommand").asString(),
        )
        assertEquals(
            "docker compose exec self-agent sard-agent repo password self --reveal --config /etc/sard/self/agent.yaml",
            selfBackup().path("passwordRevealCommand").asString(),
        )
    }

    // ---- Правило: Команду добавления репозитория собирает сервер, без секретов ----

    private fun command(
        query: String,
        session: ApiSession? = admin,
    ) = send("GET", "/api/v1/self-backup/repository-command?$query", null, session)

    @Test
    fun `Команда для локального хранилища содержит имя, адрес и перезапуск`() {
        world.selfAgent(DEFAULT_TENANT, emptyList())

        val response = command("storage=local")

        assertEquals(200, response.status, response.toString())
        assertEquals("self", response.json.path("repositoryName").asString())
        assertEquals("/var/lib/sard-self/self", response.json.path("address").asString())
        val commands =
            response.json
                .path("commands")
                .list()
                .map { it.asString() }
        assertEquals(2, commands.size)
        assertEquals("docker compose restart self-agent", commands.last())
    }

    @Test
    fun `Имя репозитория в команде - первое свободное среди репозиториев соседа`() {
        world.selfAgent(DEFAULT_TENANT)

        val response = command("storage=local")

        assertEquals("self-3", response.json.path("repositoryName").asString())
    }

    @Test
    fun `Недопустимый параметр команды отклоняется с именем поля`() {
        world.selfAgent(DEFAULT_TENANT)

        val response = command("storage=s3&provider=aws&region=eu-central-1&bucket=Acme_Sard")

        assertEquals(422, response.status)
        assertEquals("validation_failed", response.code)
        assertEquals(listOf("bucket"), response.errorFields())
    }

    @Test
    fun `Без встроенного агента команда не собирается`() {
        val response = command("storage=local")

        assertEquals(422, response.status)
        assertEquals("self_agent_missing", response.code)
    }

    @Test
    fun `Запрос команды без сессии администратора отклоняется`() {
        val response = command("storage=local", session = null)

        assertEquals(401, response.status)
    }

    @Test
    fun `Запрос команды ничего не записывает`() {
        world.selfAgent(DEFAULT_TENANT)
        val tables = listOf("sources", "self_backups", "runs", "schedules", "enrollment_tokens")
        val before = tables.map { world.count(it, DEFAULT_TENANT) }

        command("storage=s3&provider=aws&region=eu-central-1&bucket=acme-sard")

        assertEquals(before, tables.map { world.count(it, DEFAULT_TENANT) })
        assertFalse(selfBackup().path("configured").asBoolean())
    }

    // ---- Правило: Адрес хранилища без учётных данных сохраняется для карточки ----

    private fun card() = send("GET", "/api/v1/self-backup/recovery-card")

    @Test
    fun `Привязка с адресом сохраняет его для карточки`() {
        world.selfAgent(DEFAULT_TENANT)

        val bound = bind(extra = ""","location":"s3:acme-sard/prod"""")

        assertEquals(200, bound.status, bound.toString())
        assertEquals("s3:acme-sard/prod", card().json.path("location").asString())
    }

    @Test
    fun `Адрес с учётными данными или управляющими символами отклоняется`() {
        world.selfAgent(DEFAULT_TENANT)
        val addresses =
            listOf(
                "s3:https://AKIA123:secret@minio.example.net/acme",
                "sftp:backup:pw@backup.example.net:/srv/sard",
                "rest:https://user:pass@host/repo",
                "S3:https://AKIA:secret@host/b",
                "s3:HTTPS://AKIA123:secret@minio.example.net/acme",
                "https://ghp_TOKEN@host/r",
                " s3:https://AKIA:secret@h/b",
                "s3:acme-sard\\nprod",
                "x".repeat(1025),
            )
        for (address in addresses) {
            val response = bind(extra = ""","location":"$address"""")

            assertEquals(422, response.status, "$address: $response")
            assertEquals("validation_failed", response.code)
            assertEquals(listOf("location"), response.errorFields())
        }
        assertFalse(selfBackup().path("configured").asBoolean())
    }

    @Test
    fun `Допустимые адреса принимаются`() {
        world.selfAgent(DEFAULT_TENANT)
        val addresses =
            listOf(
                "sftp:backup@backup.example.net:/srv/sard",
                "sftp://backup@backup.example.net:2222//srv/sard",
                "s3:https://minio.example.net:9000/acme-sard/prod",
                "/var/lib/sard-self/self",
                "x".repeat(1024),
            )
        for (address in addresses) {
            assertEquals(200, bind(extra = ""","location":"$address"""").status, address)
        }
    }

    @Test
    fun `Привязка без адреса оставляет адрес карточки пустым`() {
        world.selfAgent(DEFAULT_TENANT)
        bind()

        assertTrue(card().json.path("location").isNull)
    }

    // ---- Правило: Карточка восстановления говорит, где хранилище и какой CA, без секретов ----

    @Test
    fun `Карточка восстановления содержит ровно поля для восстановления`() {
        world.selfAgent(DEFAULT_TENANT)
        bind(extra = ""","location":"s3:acme-sard/prod"""")
        val version = send("GET", "/api/v1/status").json.path("version").asString()
        val fingerprint = send("GET", "/api/v1/ca").json.path("fingerprint").asString()

        val response = card()

        assertEquals(200, response.status, response.toString())
        assertEquals(
            setOf(
                "repositoryName",
                "backend",
                "location",
                "repositoryId",
                "caFingerprint",
                "sardVersion",
                "generatedAt",
                "recoveryProcedure",
            ),
            response.json.propertyNames().toSet(),
        )
        assertEquals("self", response.json.path("repositoryName").asString())
        assertEquals("s3", response.json.path("backend").asString())
        assertEquals(SELF_REPOSITORY_ID, response.json.path("repositoryId").asString())
        assertEquals(fingerprint, response.json.path("caFingerprint").asString())
        assertEquals(version, response.json.path("sardVersion").asString())
        assertEquals(T0.toString(), response.json.path("generatedAt").asString())
        assertEquals(recoveryProcedureUrl(version), response.json.path("recoveryProcedure").asString())
    }

    @Test
    fun `Карточка до привязки недоступна`() {
        val response = card()

        assertEquals(409, response.status)
        assertEquals("self_backup_not_configured", response.code)
    }

    @Test
    fun `Карточка после смены хранилища описывает новый репозиторий`() {
        world.selfAgent(DEFAULT_TENANT)
        bind()

        bind("self-2")

        val json = card().json
        assertEquals("self-2", json.path("repositoryName").asString())
        assertEquals("sftp", json.path("backend").asString())
        assertEquals(SELF_REPOSITORY_ID.reversed(), json.path("repositoryId").asString())
    }

    @Test
    fun `Карточка без сессии администратора недоступна`() {
        val response = send("GET", "/api/v1/self-backup/recovery-card", session = null)

        assertEquals(401, response.status)
    }

    // ---- Правило: Смена хранилища после онбординга снова требует первого бэкапа и подтверждения ----

    @Test
    fun `Новая привязка после онбординга просит подтвердить копию ключей заново`() {
        readyToConfirm()
        confirm()
        clock.now = T0 + HOUR
        admin = world.admin(DEFAULT_TENANT)

        bind("self-2")

        val state = selfBackup()
        assertFalse(state.path("firstBackupDone").asBoolean())
        assertTrue(state.path("keysConfirmedAt").isNull)
        assertTrue(
            state
                .path("warnings")
                .list()
                .map { it.asString() }
                .contains("keys_unconfirmed"),
        )
        assertEquals("none", restriction())
        val refused = confirm()
        assertEquals("first_backup_pending", refused.code)
    }

    @Test
    fun `Первые бэкапы и подтверждение для нового хранилища снимают предупреждение`() {
        readyToConfirm()
        confirm()
        clock.now = T0 + HOUR
        admin = world.admin(DEFAULT_TENANT)
        bind("self-2")
        backUp()

        val response = confirm()

        assertEquals(204, response.status)
        val state = selfBackup()
        assertEquals((T0 + HOUR).toString(), state.path("keysConfirmedAt").asString())
        assertFalse(
            state
                .path("warnings")
                .list()
                .map { it.asString() }
                .contains("keys_unconfirmed"),
        )
    }

    @Test
    fun `До завершения онбординга предупреждения keys_unconfirmed нет`() {
        readyToConfirm()

        assertFalse(
            selfBackup()
                .path("warnings")
                .list()
                .map { it.asString() }
                .contains("keys_unconfirmed"),
        )
    }

    @Test
    fun `Завершение онбординга обновлённой установки открывает выпуск токенов`() {
        jdbc.markUpgraded()
        readyToConfirm()

        assertEquals(204, confirm().status)

        assertEquals("none", restriction())
        assertEquals(201, send("POST", "/api/v1/enrollment-tokens", "{}").status)
    }
}
