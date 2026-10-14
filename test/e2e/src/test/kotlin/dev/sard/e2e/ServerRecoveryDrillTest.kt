// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import org.junit.jupiter.api.MethodOrderer
import org.junit.jupiter.api.Order
import org.junit.jupiter.api.TestMethodOrder
import org.junit.jupiter.api.extension.RegisterExtension
import java.nio.file.Files
import java.time.Duration
import java.time.Instant
import java.time.LocalDate
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

/**
 * F7, the scenarios tagged `@drill` of docs/specs/server/server-recovery.feature (Р39–Р41): the page
 * docs/operator/12-server-recovery.md is followed as written. The old machine (Р40) is built and destroyed in the
 * extension; the blocks of the page marked `<!-- drill: id -->` run on the new one, an empty machine without a way to
 * the registries, in the order of the page, each method taking them up to the point its scenario looks at. The methods
 * share the two machines and run in order. The time and the sizes go to `build/recovery-drill/result.json` and
 * `result.md`; the duration is not asserted (a shared runner), a person moves the row to the page (OQ-226).
 */
@TestMethodOrder(MethodOrderer.OrderAnnotation::class)
class ServerRecoveryDrillTest {
    companion object {
        @JvmField
        @RegisterExtension
        val drill = RecoveryDrillStand()

        private val DB_COUNTS =
            "select (select count(*) from agents) || ' ' || (select count(*) from sources) || ' ' || (select count(*) from runs)"
        private val sizes = mutableMapOf<String, Long>()
        private val FINAL = setOf("succeeded", "failed", "cancelled")
        private val SCHEDULER_PASSES: Duration = Duration.ofSeconds(45)
    }

    private val old get() = drill.old
    private val new get() = drill.new
    private val api get() = new.api.also { new.signIn() }

    private fun state() = api.get("/api/v1/self-backup").json

    private fun warnings() = state()["warnings"].items.map { it.text }

    private fun compose(script: String) = new.vm.succeed("cd ~/sard && $script")

    private fun psql(sql: String) = compose("docker compose exec -T postgres psql -U sard -d sard -Atc \"$sql\"").trim()

    private fun agents() = api.items("/api/v1/agents")

    private fun runs() = api.items("/api/v1/runs")

    @Test
    @Order(1)
    fun `Новая ВМ получает только хранилище, пароль, карточку и пароль администратора`() {
        assertEquals(
            setOf("repositoryName", "location", "repositoryId", "caFingerprint", "sardVersion", "адрес-restic", "образ-агента", "имя-сервера"),
            new.values.keys,
        )
        assertEquals(setOf("prepare", "login"), new.typed.keys)
        assertEquals(4, new.typed.getValue("prepare").size)
        assertEquals("", new.vm.succeed("docker volume ls -q").trim(), "the new machine has volumes")
        assertEquals("", new.vm.succeed("docker images -q").trim(), "the new machine has images")
        val files = new.vm.succeed("ls -A ~").lines().filter { it.isNotBlank() }.toSet()
        assertEquals(setOf(".bashrc", ".profile", "sard-offline"), files, "the new machine has files of the old one")
        assertTrue(drill.oldMachineIsGone(), "the old machine is still there")
    }

    @Test
    @Order(2)
    fun `Новая ВМ работает без доступа в реестры образов`() {
        assertEquals("true", hostDocker("network", "inspect", drill.sard.dockerNetwork.id, "--format", "{{.Internal}}").trim())
        assertTrue(new.vm.run("curl -sS --max-time 5 -o /dev/null https://registry-1.docker.io/v2/").code != 0, "the registry answers")
        val texts = new.blocks.map { it.text }
        assertTrue(texts.none { "docker pull" in it }, "a block pulls")
        val ups = texts.flatMap { it.lines() }.filter { "docker compose up" in it }
        assertTrue(ups.isNotEmpty() && ups.all { "--pull never" in it }, "a compose up without --pull never: $ups")
    }

    @Test
    @Order(3)
    fun `Сверка id репозитория из хранилища совпадает с карточкой`() {
        new.advance("snapshots")

        val output = new.runs.getValue("snapshots").stdout
        assertTrue("repository id: ${old.card.repositoryId}\n" in output, output)
        val database = Regex("database snapshot: ([0-9a-f]+) ").find(output)!!.groupValues[1]
        val keys = Regex("keys snapshot:\\s+([0-9a-f]+) ").find(output)!!.groupValues[1]
        assertEquals(old.databaseSnapshot, database)
        assertEquals(old.keysSnapshot, keys)
    }

    @Test
    @Order(4)
    fun `Снимок ключей на новой ВМ даёт тот же CA и тот же env`() {
        new.advance("keys")

        val pki = "docker run --rm --user 0 -v sard_sard-pki:/pki:ro --entrypoint"
        val agent = "sard-agent:${old.version}-pg18"
        assertEquals(old.caCert, new.vm.succeed("$pki cat $agent /pki/ca/ca.crt"))
        assertEquals(old.caKey, new.vm.succeed("$pki cat $agent /pki/ca/ca.key"))
        assertEquals(
            "10001:10001 700 /pki/ca\n10001:10001 600 /pki/ca/ca.key\n10001:10001 600 /pki/ca/ca.crt\n",
            new.vm.succeed("$pki stat $agent -c '%u:%g %a %n' /pki/ca /pki/ca/ca.key /pki/ca/ca.crt"),
        )
        assertEquals(old.envText, new.vm.succeed("cat ~/sard/.env"))
        assertEquals("640 10001", new.vm.succeed("stat -c '%a %g' ~/sard/.env").trim())
    }

    @Test
    @Order(5)
    fun `База на новой ВМ восстанавливается из снимка в пустую базу`() {
        new.advance("database")

        val expected = old.countsAtSnapshot
        assertEquals("${expected.agents} ${expected.sources} ${expected.runs}", psql(DB_COUNTS).replace('|', ' '))
        sizes["dump"] = new.vm.succeed("stat -c %s ~/sard-recovery/dump/sard.dump").trim().toLong()
        sizes["database"] = psql("select pg_database_size('sard')").toLong()
    }

    @Test
    @Order(6)
    fun `Отпечаток CA в логе нового сервера равен отпечатку карточки`() {
        new.advance("server")

        assertTrue("CA fingerprint=${old.card.caFingerprint} origin=existing" in compose("docker compose logs server"))
        assertEquals(old.card.sardVersion, api.get("/api/v1/status").json["version"].text)
        assertEquals(old.version, old.card.sardVersion)
    }

    @Test
    @Order(7)
    fun `Восстановленный сервер видит завершённый онбординг и принимает прежний пароль администратора`() {
        new.advance("login")

        assertTrue("\"restriction\":\"none\"" in new.runs.getValue("server").stdout)
        assertEquals("204", new.runs.getValue("login").stdout.trim())
    }

    @Test
    @Order(8)
    fun `Сосед новой ВМ ждёт, пока прежний встроенный агент не отозван`() {
        new.advance("self-agent-wait")

        assertEquals(listOf("db-password"), compose("docker compose exec -T server ls /var/lib/sard/self").lines().filter { it.isNotBlank() })
        assertEquals("1", new.runs.getValue("self-agent-wait").stdout.lines().first { it.isNotBlank() }.trim())
    }

    @Test
    @Order(9)
    fun `После отзыва прежнего встроенного агента сосед новой ВМ регистрируется встроенным`() {
        new.advance("revoke-x")

        val all = agents()
        val x = all.single { it["id"].text == old.builtinAgentId }
        assertTrue(!x["revokedAt"].isNull, "the previous built-in agent is not revoked")
        val live = all.filter { it["builtin"].bool && it["revokedAt"].isNull }
        assertEquals(1, live.size)
        assertNotEquals(old.builtinAgentId, live.single()["id"].text)
        assertEquals("online", live.single()["status"].text)
    }

    @Test
    @Order(10)
    fun `До перепривязки самобэкап на новой ВМ предупреждает о замене агента`() {
        assertTrue("agent_replaced" in warnings(), warnings().toString())
    }

    @Test
    @Order(11)
    fun `Сосед новой ВМ подключает существующий репозиторий самобэкапа`() {
        new.advance("repo-add")

        val repository = state()["repositories"].items.single { it["name"].text == old.card.repositoryName }
        assertEquals(old.card.repositoryId, repository["repositoryId"].text)
    }

    @Test
    @Order(12)
    fun `Перепривязка с адресом карточки сохраняет адрес в новой карточке`() {
        new.advance("rebind")

        assertEquals(old.card.location, api.get("/api/v1/self-backup/recovery-card").json["location"].text)
        val sources = state()["sources"].items.associate { it["role"].text!! to it["sourceId"].text!! }
        assertEquals(mapOf("self_database" to old.sourceIds.getValue("self_database"), "self_keys" to old.sourceIds.getValue("self_keys")), sources)
    }

    @Test
    @Order(13)
    fun `После первых бэкапов на новой ВМ остаётся только просьба подтвердить копию ключей`() {
        new.advance("backup-now")

        assertEquals(listOf("keys_unconfirmed"), warnings())
        assertTrue(state()["sources"].items.all { it["lastRun"]["status"].text == "succeeded" })
    }

    @Test
    @Order(14)
    fun `Новые снимки самобэкапа ложатся в тот же репозиторий`() {
        val runIds = state()["sources"].items.map { it["lastRun"]["id"].text!! }
        assertEquals(2, runIds.size)
        for (run in runIds) {
            val snapshots = new.vm.succeed("~/sard-recovery/restic snapshots --json --tag sard.run=$run")
            assertTrue("\"sard.run=$run\"" in snapshots, "no snapshot of run $run in the repository: $snapshots")
        }
    }

    @Test
    @Order(15)
    fun `Подтверждение копии ключей на новой ВМ снимает предупреждения самобэкапа`() {
        new.advance("confirm-keys")

        assertEquals(emptyList(), warnings())
        assertEquals("ok", state()["status"].text)
    }

    @Test
    @Order(16)
    fun `Агенты, зарегистрированные до снимка базы, подключаются без перерегистрации и перезапуска`() {
        new.advance("agents-online")

        val online = agents().filter { it["status"].text == "online" }.map { it["id"].text }
        for (label in listOf("A1", "A2")) {
            val agent = old.agents.getValue(label)
            assertTrue(agent.id in online, "$label is not online with its id")
            assertEquals(agent.startedAt, agent.container.currentContainerInfo.state.startedAt, "$label was restarted")
            assertEquals(agent.keyPem, String(agent.host.read(AgentHost.KEY_FILE)))
            assertEquals(agent.certPem, String(agent.host.read(AgentHost.CERT_FILE)))
        }
    }

    @Test
    @Order(17)
    fun `Агент, зарегистрированный после снимка базы, получает CERT_UNKNOWN и останавливается`() {
        val a3 = old.agents.getValue("A3").container

        Await.until("the container of A3 exits", Duration.ofMinutes(3)) { a3.currentContainerInfo.state.running != true }

        assertEquals(78L, a3.currentContainerInfo.state.exitCodeLong)
        assertTrue("reason CERT_UNKNOWN" in a3.logs, a3.logs)
    }

    @Test
    @Order(18)
    fun `Агент после CERT_UNKNOWN регистрируется заново командами страницы`() {
        new.advance("reenroll")

        assertTrue("systemctl restart sard-agent" in new.runs.getValue("reenroll").stdout)
        val host = old.agents.getValue("A3").host
        drill.sard.track("agent-A3-again", host.agent()).start()
        val again =
            Await.value("A3 online with a new id", Duration.ofMinutes(3)) {
                agents().firstOrNull { it["hostname"].text == host.hostname && it["status"].text == "online" }?.get("id")?.text
            }
        assertNotEquals(old.agents.getValue("A3").id, again)
    }

    @Test
    @Order(19)
    fun `История до снимка базы на месте`() {
        val newAgents = agents().associate { it["id"].text to it["revokedAt"].text }
        for (label in listOf("A1", "A2")) assertTrue(old.agents.getValue(label).id in newAgents, "agent $label is lost")
        assertTrue(old.builtinAgentId in newAgents)
        val sources = api.items("/api/v1/sources").map { it["id"].text }
        assertTrue(old.sourceIds.values.filter { it.isNotEmpty() }.all { it in sources }, "a source is lost: $sources")
        val now = runs().associateBy { it["id"].text }
        val before = old.runs.filter { it.finishedAt != null && Instant.parse(it.finishedAt) < old.queuedAt && it.id !in old.selfRuns.values }
        assertTrue(before.isNotEmpty())
        for (run in before) {
            val found = now[run.id]
            assertTrue(found != null, "run ${run.id} is lost")
            assertEquals(run.status, found["status"].text)
            assertEquals(run.finishedAt, found["finishedAt"].text)
        }
    }

    @Test
    @Order(20)
    fun `Запуск после снимка базы в истории отсутствует`() {
        assertTrue(runs().none { it["id"].text == old.afterSnapshotRun })
    }

    @Test
    @Order(21)
    fun `Запуск самобэкапа базы, шедший в момент снимка, становится failed с потерянным шагом`() {
        assertLost("self_database")
    }

    @Test
    @Order(22)
    fun `Запуск самобэкапа ключей, шедший или ждавший в момент снимка, становится failed`() {
        assertLost("self_keys")
    }

    private fun assertLost(role: String) {
        val run = old.selfRuns.getValue(role)
        val failed = Await.value("run $run failed", Duration.ofMinutes(3)) { api.get("/api/v1/runs/$run").json.takeIf { it["status"].text == "failed" } }
        assertEquals(listOf("lost"), failed["steps"].items.map { it["status"].text })
    }

    @Test
    @Order(23)
    fun `Расписание источника A1 даёт один догоняющий запуск`() {
        val source = old.sourceIds.getValue("A1")
        fun catchUps() = api.items("/api/v1/runs?sourceId=$source").filter { it["trigger"].text == "catch_up" }
        Await.until("the catch-up run of A1", Duration.ofMinutes(4)) { catchUps().isNotEmpty() }
        Thread.sleep(SCHEDULER_PASSES.toMillis())

        val found = catchUps()
        assertEquals(1, found.size)
        assertTrue(found.single()["catchUp"]["missedCount"].int >= 2)
    }

    @Test
    @Order(24)
    fun `Новый бэкап обычного источника на восстановленном сервере проходит`() {
        val run = api.created("/api/v1/sources/${old.sourceIds.getValue("A2")}/runs").json["id"].text!!

        val status = Await.value("run $run finished", Duration.ofMinutes(3)) { api.get("/api/v1/runs/$run").json["status"].text?.takeIf { it in FINAL } }

        assertEquals("succeeded", status)
    }

    @Test
    @Order(25)
    fun `Каждый помеченный блок страницы выполняется на новой ВМ без ошибки`() {
        new.advance(new.blocks.last().id)

        assertEquals(new.blocks.map { it.id }, new.runs.keys.toList())
        assertTrue(new.runs.values.all { it.code == 0 })
    }

    @Test
    @Order(26)
    fun `Секреты учений не попадают в команды и логи`() {
        val secrets = listOf(old.resticPassword, old.s3Key.secret, old.adminPassword)
        val containers = old.agents.values.map { "the log of agent ${it.label}" to it.container.logs } + ("the log of the storage" to drill.garage.container.logs)

        assertEquals(emptyList(), new.leaks(secrets))
        for ((what, log) in containers) assertTrue(secrets.none { it in log }, "$what shows a secret of the drill")
    }

    @Test
    @Order(27)
    fun `Учения записывают время восстановления и размер базы`() {
        val result =
            DrillResult(
                date = LocalDate.now(),
                version = old.version,
                dumpBytes = checkNotNull(sizes["dump"]),
                databaseBytes = checkNotNull(sizes["database"]),
                secondsToServer = new.secondsToServer,
                secondsToAgents = new.secondsToAgents,
                secondsToSelfBackup = new.secondsToSelfBackup,
            )
        val dir = E2e.recoveryDrillDir
        Files.writeString(dir.resolve("result.json"), result.json())
        Files.writeString(dir.resolve("result.md"), result.markdown())

        assertTrue(Files.readString(dir.resolve("result.json")).contains("\"minutesToSelfBackup\""))
        assertTrue(new.secondsToServer > 0 && new.secondsToAgents >= new.secondsToServer && new.secondsToSelfBackup > 0)
    }

    private fun hostDocker(vararg args: String): String {
        val process = ProcessBuilder(listOf("docker") + args).redirectErrorStream(true).start()
        val output = process.inputStream.bufferedReader().readText()
        check(process.waitFor() == 0) { "docker ${args.first()} failed: $output" }
        return output
    }
}
