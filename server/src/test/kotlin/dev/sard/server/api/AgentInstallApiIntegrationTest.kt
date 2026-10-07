// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.install.SignedRelease
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.registration.Registration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.beans.factory.annotation.Value
import org.springframework.boot.test.web.server.LocalServerPort
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.TestPropertySource
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

private const val DEB = "sard-agent_1.4.0_amd64.deb"
private const val BASE = "/downloads/agent/"

/** docs/specs/server/agent-install.feature: a server of v1.4.0 serving the signed release v1.4.0, restic 0.19.1. */
@RestApiTest
@Import(SignedRelease::class)
@TestPropertySource(properties = ["sard.pki.server-names=sard.corp.example,localhost,127.0.0.1,::1"])
class AgentInstallApiIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired registration: Registration,
    @Autowired jdbc: JdbcTemplate,
    @Autowired clock: MovableClock,
    @Autowired mapper: ObjectMapper,
    @LocalServerPort port: Int,
    @Value("\${local.grpc.server.port}") grpc: Int,
    @Value("\${spring.grpc.server.port}") private val grpcSetting: Int,
    @Value("\${server.port}") private val httpSetting: Int,
) {
    private val world = RestWorld(ca, enrollment, tokens, registration, jdbc, clock, mapper, port, grpc)
    private val tenant = world.tenant()
    private val admin = world.admin(tenant)
    private val baseUrl = "http://sard.corp.example:$httpSetting"

    @AfterTest
    fun `drop the tenants`() = world.close()

    private fun install(query: String = "") = world.api.get("/api/v1/agent-install$query", admin)

    private fun upgrade(
        agent: UUID,
        query: String = "",
        session: ApiSession? = admin,
    ) = world.api.get("/api/v1/agents/$agent/upgrade$query", session)

    private fun steps(response: ApiResponse): List<JsonNode> = response.json.path("steps").list()

    private fun kinds(response: ApiResponse) = steps(response).map { it.path("kind").asString() }

    private fun commands(
        response: ApiResponse,
        kind: String,
    ): List<String> =
        steps(response)
            .single { it.path("kind").asString() == kind }
            .path("commands")
            .list()
            .map { it.asString() }

    @Test
    fun `Без сессии сведения об установке не отдаются`() {
        val response = world.api.get("/api/v1/agent-install", null)

        assertEquals(401, response.status)
        assertEquals("unauthenticated", response.code)
    }

    @Test
    fun `Без параметров выбраны deb и amd64`() {
        val response = install()

        assertEquals(200, response.status)
        assertEquals("deb", response.json.path("format").asString())
        assertEquals("amd64", response.json.path("arch").asString())
        assertEquals(
            listOf("curl -fsSLO $baseUrl${BASE}$DEB") + commands(response, "download").drop(1),
            commands(response, "download"),
        )
    }

    @Test
    fun `Выбор arm64 и архива даёт архив для arm64`() {
        val response = install("?arch=arm64&format=tar")

        val file = commands(response, "download").first().substringAfterLast('/')
        assertEquals("sard-agent_v1.4.0_linux_arm64.tar.gz", file)
        assertEquals("arm64", response.json.path("arch").asString())
        assertEquals("tar", response.json.path("format").asString())
    }

    @Test
    fun `Выбор rpm для amd64 даёт rpm и установку через rpm Uvh`() {
        val response = install("?arch=amd64&format=rpm")

        assertEquals(200, response.status)
        assertEquals("rpm", response.json.path("format").asString())
        assertEquals("sard-agent-1.4.0-1.amd64.rpm", commands(response, "download").first().substringAfterLast('/'))
        assertEquals(listOf("sudo rpm -Uvh sard-agent-1.4.0-1.amd64.rpm"), commands(response, "install"))
    }

    @Test
    fun `Неизвестная архитектура отклоняется`() {
        val response = install("?arch=riscv64")

        assertEquals(422, response.status)
        assertEquals(listOf("arch"), response.errorFields())
    }

    @Test
    fun `Неизвестный формат отклоняется`() {
        for (format in listOf("zip", "RPM")) {
            val response = install("?format=$format")

            assertEquals(422, response.status, format)
            assertEquals(listOf("format"), response.errorFields(), format)
        }
    }

    @Test
    fun `Неизвестная утилита скачивания отклоняется`() {
        val response = install("?fetch=ftp")

        assertEquals(422, response.status)
        assertEquals(listOf("fetch"), response.errorFields())
    }

    @Test
    fun `Ответ называет версию агента и restic из манифеста`() {
        val json = install().json

        assertEquals("v1.4.0", json.path("agentVersion").asString())
        assertEquals("0.19.1", json.path("resticVersion").asString())
        assertTrue(json.path("downloadsEnabled").asBoolean())
    }

    @Test
    fun `Шаги deb идут в установленном порядке, необязательна только проверка подписи`() {
        val response = install()

        assertEquals(
            listOf("download", "checksum", "signature", "install", "configure", "enroll", "repo-init", "start"),
            kinds(response),
        )
        for (step in steps(response)) {
            assertEquals(step.path("kind").asString() == "signature", step.path("optional").asBoolean())
        }
    }

    @Test
    fun `Скачивание забирает пакет, SHA256SUMS и подпись с адреса раздачи`() {
        val download = commands(install(), "download")

        assertEquals(
            listOf(
                "curl -fsSLO $baseUrl$BASE$DEB",
                "curl -fsSLO $baseUrl${BASE}SHA256SUMS",
                "curl -fsSLO $baseUrl${BASE}SHA256SUMS.minisig",
            ),
            download,
        )
    }

    @Test
    fun `Выбор wget даёт команды скачивания через wget`() {
        val download = commands(install("?fetch=wget"), "download")

        assertEquals(3, download.size)
        assertTrue(download.all { it.startsWith("wget ") && "curl" !in it }, download.toString())
    }

    @Test
    fun `Проверка суммы сверяет только скачанный пакет`() {
        assertEquals(listOf("grep '  $DEB\$' SHA256SUMS | sha256sum -c -"), commands(install(), "checksum"))
    }

    @Test
    fun `Установка из архива создаёт пользователя и ставит юнит`() {
        val install = commands(install("?format=tar"), "install")

        assertTrue(install.any { "useradd" in it && "sard-agent" in it }, install.toString())
        assertTrue(install.any { it.contains("/usr/lib/sard/") && it.contains("restic") }, install.toString())
        assertTrue(install.any { "/usr/lib/systemd/system/sard-agent.service" in it }, install.toString())
    }

    @Test
    fun `Регистрация и создание репозитория выполняются от имени пользователя службы`() {
        val response = install()

        assertEquals(1, commands(response, "enroll").size)
        assertTrue(commands(response, "enroll").single().startsWith("sudo -u sard-agent sard-agent enroll "))
        assertEquals(
            listOf("sudo -u sard-agent sard-agent repo init --generate-password main"),
            commands(response, "repo-init"),
        )
        assertEquals(listOf("sudo systemctl enable --now sard-agent.service"), commands(response, "start"))
    }

    @Test
    fun `Шаг enroll совпадает с командой из ответа создания токена, кроме строки токена`() {
        val created = world.api.post("/api/v1/enrollment-tokens", admin, "{}").json
        val command = created.path("enrollCommand").asString()
        val token = created.path("token").asString()

        val enroll = commands(install(), "enroll").single()

        assertEquals(command.replace(token, "<TOKEN>"), enroll)
        assertTrue(enroll.startsWith("sudo -u sard-agent sard-agent enroll --server "), enroll)
    }

    @Test
    fun `Без SARD_AGENT_ENDPOINT в enroll стоит первое имя сертификата`() {
        val enroll = commands(install(), "enroll").single()

        assertTrue("--server sard.corp.example:$grpcSetting " in enroll, enroll)
    }

    @Test
    fun `Адрес раздачи по умолчанию строится из хоста AgentEndpoint и порта HTTP`() {
        val links = commands(install(), "download")

        assertTrue(links.all { " http://sard.corp.example:$httpSetting/downloads/agent/" in it }, links.toString())
    }

    @Test
    fun `Заголовки Host и X-Forwarded-Host не влияют на адреса в командах`() {
        val response =
            world.api.send(
                "GET",
                "/api/v1/agent-install",
                admin,
                headers = mapOf("Host" to "evil.example", "X-Forwarded-Host" to "evil.example"),
            )

        assertEquals(200, response.status)
        assertFalse("evil.example" in response.body, response.body)
    }

    @Test
    fun `Команда enroll содержит заполнитель вместо токена`() {
        val token =
            world.api
                .post("/api/v1/enrollment-tokens", admin)
                .json
                .path("token")
                .asString()

        val response = install()

        assertTrue("--token <TOKEN>" in commands(response, "enroll").single())
        assertFalse("sard_" in response.body, response.body)
        assertFalse(token in response.body)
    }

    @Test
    fun `Ответ называет ID и строку ключа релизов`() {
        val key = install().json.path("releaseKey")

        assertEquals("DF5D5B6DB257DBFA", key.path("id").asString())
        assertEquals(
            java.io.File("../deploy/release/sard-release.pub").readLines()[1],
            key.path("publicKey").asString(),
        )
    }

    @Test
    fun `Шаг подписи проверяет SHA256SUMS ключом, переданным строкой`() {
        val response = install()

        assertEquals(
            listOf("minisign -Vm SHA256SUMS -P ${response.json.path("releaseKey").path("publicKey").asString()}"),
            commands(response, "signature"),
        )
        assertTrue(response.json.path("signed").asBoolean())
    }

    @Test
    fun `Ответ ведёт на документацию о ручной установке`() {
        assertTrue(
            install()
                .json
                .path("manualInstallDoc")
                .asString()
                .endsWith("docs/operations/agent-install.md"),
        )
    }

    @Test
    fun `Обновление берёт архитектуру из последнего Register агента`() {
        val agent = world.agent(tenant, snapshotOf(arch = "arm64"))

        val response = upgrade(agent.agentId, "?format=deb")

        assertEquals(200, response.status)
        assertEquals("arm64", response.json.path("arch").asString())
        assertEquals(
            "sard-agent_1.4.0_arm64.deb",
            commands(response, "download").first().substringAfterLast('/'),
        )
        assertTrue(response.json.path("reason").isNull)
    }

    @Test
    fun `Обновление rpm берёт архитектуру из последнего Register агента`() {
        val agent = world.agent(tenant, snapshotOf(arch = "arm64"))

        val response = upgrade(agent.agentId, "?format=rpm")

        assertEquals(200, response.status)
        assertEquals(
            "sard-agent-1.4.0-1.arm64.rpm",
            commands(response, "download").first().substringAfterLast('/'),
        )
    }

    @Test
    fun `Обновление deb ставит пакет поверх без удаления`() {
        val agent = world.agent(tenant, snapshotOf(arch = "amd64"))

        val response = upgrade(agent.agentId)

        assertEquals(listOf("download", "checksum", "signature", "upgrade"), kinds(response))
        assertEquals(listOf("sudo dpkg -i $DEB"), commands(response, "upgrade"))
        assertFalse("/etc/sard" in response.body)
    }

    @Test
    fun `Обновление rpm ставит пакет поверх через rpm Uvh без удаления`() {
        val agent = world.agent(tenant, snapshotOf(arch = "amd64"))

        val response = upgrade(agent.agentId, "?format=rpm")

        assertEquals(listOf("download", "checksum", "signature", "upgrade"), kinds(response))
        assertEquals(listOf("sudo rpm -Uvh sard-agent-1.4.0-1.amd64.rpm"), commands(response, "upgrade"))
    }

    @Test
    fun `Обновление из архива заменяет бинарники и перезапускает работающую службу`() {
        val agent = world.agent(tenant, snapshotOf(arch = "amd64"))

        val response = upgrade(agent.agentId, "?format=tar")

        assertEquals(listOf("download", "checksum", "signature", "upgrade", "restart"), kinds(response))
        assertEquals(listOf("sudo systemctl try-restart sard-agent.service"), commands(response, "restart"))
    }

    @Test
    fun `Агент на архитектуре без пакета получает объяснение вместо команд`() {
        val agent = world.agent(tenant, snapshotOf(arch = "386"))

        val response = upgrade(agent.agentId)

        assertEquals(200, response.status)
        assertEquals(emptyList(), steps(response))
        assertEquals("arch_unavailable", response.json.path("reason").asString())
    }

    @Test
    fun `Агент без Register не получает команд обновления`() {
        val agent = world.enroll(tenant)

        val response = upgrade(agent.agentId)

        assertEquals(200, response.status)
        assertEquals(emptyList(), steps(response))
        assertEquals("arch_unknown", response.json.path("reason").asString())
        assertTrue(response.json.path("arch").isNull)
    }

    private fun keeps(
        arch: String,
        format: String,
    ) = upgrade(world.agent(tenant, snapshotOf(arch = arch)).agentId, "?format=$format")
        .json
        .path("keepsConfiguration")
        .asBoolean(true)

    @Test
    fun `Обновление deb сохраняет конфигурацию и ключи`() = assertTrue(keeps("amd64", "deb"))

    @Test
    fun `Обновление rpm сохраняет конфигурацию и ключи`() = assertTrue(keeps("amd64", "rpm"))

    @Test
    fun `Обновление из архива не обещает сохранить конфигурацию и ключи`() = assertFalse(keeps("amd64", "tar"))

    @Test
    fun `Признак сохранения конфигурации не зависит от архитектуры`() {
        assertEquals(listOf(true, true, false), listOf("deb", "rpm", "tar").map { keeps("arm64", it) })
    }

    @Test
    fun `Обновление без команд для архитектуры без пакета не обещает сохранить конфигурацию`() {
        val agent = world.agent(tenant, snapshotOf(arch = "386"))

        val json = upgrade(agent.agentId, "?format=deb").json

        assertEquals("arch_unavailable", json.path("reason").asString())
        assertFalse(json.path("keepsConfiguration").asBoolean(true))
    }

    @Test
    fun `Обновление агента без Register не обещает сохранить конфигурацию`() {
        val agent = world.enroll(tenant)

        val json = upgrade(agent.agentId, "?format=deb").json

        assertEquals("arch_unknown", json.path("reason").asString())
        assertFalse(json.path("keepsConfiguration").asBoolean(true))
    }

    @Test
    fun `Обновление неизвестного агента отвечает 404`() {
        val response = upgrade(UUID.randomUUID())

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
    }

    @Test
    fun `Обновление агента другого тенанта отвечает 404`() {
        val agent = world.agent(world.tenant(), snapshotOf())

        val response = upgrade(agent.agentId)

        assertEquals(404, response.status)
        assertEquals("not_found", response.code)
    }

    @Test
    fun `Обновление без сессии отвечает 401`() {
        assertEquals(401, upgrade(UUID.randomUUID(), session = null).status)
    }

    @Test
    fun `Обновление с неизвестным форматом отвечает 422`() {
        val agent = world.agent(tenant, snapshotOf())

        val response = upgrade(agent.agentId, "?format=zip")

        assertEquals(422, response.status)
        assertEquals(listOf("format"), response.errorFields())
    }

    @Test
    fun `Агент v1_3_2 при раздаче v1_4_0 устарел, v1_4_0 и v1_5_0 - нет`() {
        val old = world.agent(tenant, snapshotOf(version = "v1.3.2"))
        val same = world.agent(tenant, snapshotOf(version = "v1.4.0"))
        val newer = world.agent(tenant, snapshotOf(version = "v1.5.0"))
        val gitBuild = world.agent(tenant, snapshotOf(version = "v1.3.2-5-gabc1234-dirty"))
        val never = world.enroll(tenant)

        fun outdated(agent: UUID) =
            world.api
                .get("/api/v1/agents?limit=200", admin)
                .json
                .path("items")
                .list()
                .first { it.path("id").asString() == agent.toString() }
                .path("outdated")
                .asBoolean()

        assertTrue(outdated(old.agentId))
        assertFalse(outdated(same.agentId))
        assertFalse(outdated(newer.agentId))
        assertFalse(outdated(gitBuild.agentId))
        assertFalse(outdated(never.agentId))
        assertTrue(
            world.api
                .get("/api/v1/agents/${old.agentId}", admin)
                .json
                .path("outdated")
                .asBoolean(),
        )
    }
}
