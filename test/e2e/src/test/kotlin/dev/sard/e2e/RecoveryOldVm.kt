// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.nio.file.Path
import java.security.SecureRandom
import java.time.Duration
import java.time.Instant
import java.util.HexFormat

/** The card of the recovery (GET /api/v1/self-backup/recovery-card), the fields the page substitutes. */
internal class Card(
    val repositoryName: String,
    val location: String,
    val repositoryId: String,
    val caFingerprint: String,
    val sardVersion: String,
)

/** A run as the old machine's list had it. */
internal class RunRecord(
    val id: String,
    val sourceId: String,
    val trigger: String,
    val status: String,
    val queuedAt: String,
    val finishedAt: String?,
)

/** An agent host of the drill, as the old machine knew it. */
internal class DrillAgent(
    val label: String,
    val id: String,
    val host: AgentHost,
    val container: org.testcontainers.containers.GenericContainer<*>,
    val startedAt: String,
    val keyPem: String,
    val certPem: String,
)

/**
 * What the old machine (Р40) leaves behind when it is destroyed: the recovery card, the secrets only the owner has
 * (the repository password, the storage keys, the administrator's password), the lists of agents, sources and runs, and
 * the CA the new machine must get back.
 */
internal class OldInstallation(
    val version: String,
    val adminPassword: String,
    val resticPassword: String,
    val s3Key: GarageS3.Key,
    val card: Card,
    val envText: String,
    val caCert: String,
    val caKey: String,
    val agents: Map<String, DrillAgent>,
    val builtinAgentId: String,
    val sourceIds: Map<String, String>,
    val selfRuns: Map<String, String>,
    val databaseSnapshot: String,
    val keysSnapshot: String,
    val queuedAt: Instant,
    val countsAtSnapshot: Counts,
    val runs: List<RunRecord>,
    val afterSnapshotRun: String,
) {
    class Counts(
        val agents: Int,
        val sources: Int,
        val runs: Int,
    )
}

/**
 * The old machine of the drill (Р40): the installation of deploy/docker-compose.yml with the images of the build, the
 * wizard of F4a, the self-backup into the storage outside both machines with the key copy confirmed, two ordinary agents
 * A1 (a schedule every minute) and A2 (none) with at least three finished runs each; then "run now" of the self-backup
 * (snapshots D and K) in the first half of a minute, when A1 and A2 have no active run, the agent A3 and one more run of
 * A1, which belong to the history after the snapshot. This is test code, not the page.
 */
internal class RecoveryOldVm(
    private val sard: SardEnvironment,
    private val garage: GarageS3,
    private val garageEndpoint: String,
    private val offline: Path,
    private val version: String,
) {
    private val random = SecureRandom()
    private val adminPassword = hex().also(sard::secret)
    private val dbPassword = hex().also(sard::secret)
    private lateinit var vm: RecoveryVm
    private lateinit var api: VmApi

    private fun hex() = HexFormat.of().formatHex(ByteArray(16).also(random::nextBytes))

    fun build(): OldInstallation {
        vm = RecoveryVm(sard, "vm-old", offline, listOf(SERVER_NAME)).start()
        api = VmApi(vm)
        vm.succeed("gunzip -c ~/sard-offline/sard-*-images-linux-*.tar.gz | docker load", timeout = Duration.ofMinutes(10))
        val envText = install()
        wizard()
        val key = garage.key("drill", BUCKET, write = true)
        val resticPassword = selfBackup(key)
        val card = card()
        val a1 = agent("A1", schedule = true)
        val a2 = agent("A2", schedule = false)
        val sources = mutableMapOf("A1" to a1.second, "A2" to a2.second)
        threeRuns(a1.second, a2.second)
        val (selfRuns, queuedAt) = snapshotRun(a1.second)
        val counts = OldInstallation.Counts(agents().size, sources().size, runs().size)
        val snapshots = selfSnapshots(selfRuns)
        val a3 = agent("A3", schedule = false, source = false)
        val after = runAfter(sources.getValue("A1"), queuedAt)
        val selfSources = selfSources()
        val x = checkNotNull(api.get("/api/v1/self-backup").json["agentId"].text)
        val old =
            OldInstallation(
                version = version,
                adminPassword = adminPassword,
                resticPassword = resticPassword,
                s3Key = key,
                card = card,
                envText = envText,
                caCert = vm.succeed(readInVolume("/pki/ca/ca.crt")),
                caKey = vm.succeed(readInVolume("/pki/ca/ca.key")).also(sard::secret),
                agents = mapOf("A1" to a1.first, "A2" to a2.first, "A3" to a3.first),
                builtinAgentId = x,
                sourceIds = sources + selfSources,
                selfRuns = selfRuns,
                databaseSnapshot = snapshots.first,
                keysSnapshot = snapshots.second,
                queuedAt = queuedAt,
                countsAtSnapshot = counts,
                runs = runs(),
                afterSnapshotRun = after,
            )
        sard.secret(old.envText.lines().first { it.startsWith("SARD_DB_PASSWORD=") }.substringAfter('='))
        return old
    }

    fun destroy() = vm.destroy()

    fun isGone() = vm.isGone()

    private fun readInVolume(path: String) =
        "docker run --rm --user 0 -v sard_sard-pki:/pki:ro --entrypoint cat sard-agent:$version-pg18 $path"

    private fun install(): String {
        val env =
            """
            SARD_VERSION=$version
            SARD_IMAGE=sard-server
            SARD_AGENT_IMAGE=sard-agent
            SARD_DB_PASSWORD=$dbPassword
            SARD_PKI_SERVER_NAMES=localhost,127.0.0.1,$SERVER_NAME
            SARD_AGENT_ENDPOINT=$SERVER_NAME:9090
            """.trimIndent() + "\n"
        vm.succeed("mkdir -p ~/sard")
        vm.put("/root/sard/docker-compose.yml", java.nio.file.Files.readAllBytes(E2e.composeFile))
        vm.put("/root/sard/.env", env.toByteArray(), TarFiles.OWNER_ONLY)
        vm.succeed("cd ~/sard && chgrp 10001 .env && chmod 640 .env && docker compose up -d --wait --pull never", timeout = Duration.ofMinutes(5))
        return env
    }

    private fun wizard() {
        val code =
            Await.value("the setup code in the log of the old server") {
                SetupCode.lastIn(vm.succeed("cd ~/sard && docker compose logs server"))
            }
        sard.secret(code)
        check(api.post("/api/v1/onboarding/setup-session", """{"code":"$code"}""").status == VmApi.NO_CONTENT)
        check(api.post("/api/v1/onboarding/ca").status == VmApi.NO_CONTENT)
        check(api.post("/api/v1/onboarding/admin", """{"password":"$adminPassword"}""").status == VmApi.NO_CONTENT)
        api.login(adminPassword)
    }

    /** repo add of a new repository in the bucket, the binding, the first backups, the key copy; returns the password. */
    private fun selfBackup(key: GarageS3.Key): String {
        Await.until("the built-in agent of the old server", Duration.ofMinutes(2)) { !api.get("/api/v1/self-backup").json["agentId"].isNull }
        vm.put("/root/s3.secret", key.secret.toByteArray(), TarFiles.OWNER_ONLY)
        vm.succeed(
            "cd ~/sard && docker compose run --rm --no-deps --pull never -u 0 -v /root/s3.secret:/s3.secret:ro " +
                "--cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER --cap-add SETUID --cap-add SETGID " +
                "self-agent repo add self s3:$garageEndpoint/$BUCKET --access-key-id ${key.id} --region ${GarageS3.REGION} " +
                "--secret-key-from-file /s3.secret --no-restart --config /etc/sard/self/agent.yaml && " +
                "docker compose restart self-agent",
            timeout = Duration.ofMinutes(3),
        )
        val password =
            vm
                .succeed("cd ~/sard && docker compose exec -T self-agent sard-agent repo password self --reveal --config /etc/sard/self/agent.yaml")
                .trimEnd('\n')
                .also(sard::secret)
        Await.until("the repository self in the state of the self-backup", Duration.ofMinutes(2)) {
            api.get("/api/v1/self-backup").json["repositories"].items.any { it["name"].text == "self" && !it["repositoryId"].isNull }
        }
        val bound = api.put("/api/v1/self-backup/repository", """{"repositoryName":"self","location":"s3:$garageEndpoint/$BUCKET"}""")
        check(bound.status == VmApi.OK) { "binding answered ${bound.status}: ${bound.body}" }
        startSelfBackup()
        Await.until("the first backups of the old server", Duration.ofMinutes(5)) { api.get("/api/v1/self-backup").json["firstBackupDone"].bool }
        api.created("/api/v1/self-backup/keys-confirmation", """{"copiedOutsideServer":true}""", VmApi.NO_CONTENT)
        return password
    }

    private fun startSelfBackup(): Map<String, String> {
        val started = api.created("/api/v1/self-backup/runs", expected = VmApi.OK)
        return started.json["runs"].items.associate { it["role"].text!! to it["runId"].text!! }
    }

    private fun card(): Card {
        val card = api.get("/api/v1/self-backup/recovery-card").json
        return Card(
            repositoryName = card["repositoryName"].text!!,
            location = card["location"].text!!,
            repositoryId = card["repositoryId"].text!!,
            caFingerprint = card["caFingerprint"].text!!,
            sardVersion = card["sardVersion"].text!!,
        )
    }

    /** An ordinary agent on its own host outside the machine; with a source of files over [DATA] unless [source] is false. */
    private fun agent(
        label: String,
        schedule: Boolean,
        source: Boolean = true,
    ): Pair<DrillAgent, String> {
        val token = api.created("/api/v1/enrollment-tokens", """{"ttlSeconds":3600,"label":"$label"}""").json["token"].text!!
        sard.secret(token)
        val host = AgentHost(sard, hostname = label.lowercase(), local = Chain.repository("${AgentHost.STATE_DIR}/repo"), endpoint = ENDPOINT)
        val id = AgentEnroller.enroll(host, token)
        check(host.repoInit(Chain.REPOSITORY, "--generate-password").code == 0) { "repo init on $label" }
        val tree =
            TarFiles(
                listOf(
                    TarFiles.Entry("", null, TarFiles.OWNER_DIR),
                    TarFiles.Entry("hello.txt", "hello from $label\n".toByteArray(), TarFiles.READABLE),
                ),
            )
        host.put(Chain.DATA, tree)
        val container = sard.track("agent-$label", host.agent()).also { it.start() }
        Await.until("the repository of $label in Register", Duration.ofMinutes(2)) {
            api.get("/api/v1/agents/$id").json["repositories"].items.any { it["name"].text == Chain.REPOSITORY && !it["repositoryId"].isNull }
        }
        val sourceId =
            if (source) {
                val body = """{"name":"$label","agentId":"$id","plugin":"files","repositoryName":"${Chain.REPOSITORY}","config":${Chain.files(Chain.DATA)}}"""
                api.created("/api/v1/sources", body).json["id"].text!!
            } else {
                ""
            }
        if (schedule) {
            check(api.put("/api/v1/sources/$sourceId/schedule", """{"cron":"* * * * *","timezone":"UTC","enabled":true}""").status == VmApi.OK)
        }
        val started = checkNotNull(container.currentContainerInfo.state.startedAt)
        return DrillAgent(label, id, host, container, started, String(host.read(AgentHost.KEY_FILE)).also(sard::secret), String(host.read(AgentHost.CERT_FILE))) to sourceId
    }

    private fun threeRuns(
        a1: String,
        a2: String,
    ) {
        repeat(RUNS_BEFORE) {
            val run = api.created("/api/v1/sources/$a2/runs").json["id"].text!!
            awaitRun(run)
        }
        Await.until("three finished runs of A1", Duration.ofMinutes(6)) { finished(a1) >= RUNS_BEFORE }
    }

    private fun finished(source: String) = runs().count { it.sourceId == source && it.finishedAt != null }

    private fun awaitRun(run: String): String =
        Await.value("a final status of run $run", Duration.ofMinutes(3)) {
            api.get("/api/v1/runs/$run").json["status"].text?.takeIf { it in FINAL }
        }

    /**
     * "Run now" of the self-backup in the first half of a minute with no active run of A1 or A2 (the dump is taken
     * inside the run, so it has none of theirs in progress). Returns role → run and the time the runs were queued.
     */
    private fun snapshotRun(a1: String): Pair<Map<String, String>, Instant> {
        repeat(SNAPSHOT_ATTEMPTS) {
            val attempt = quietSnapshotRun()
            if (!overlaps(a1, attempt)) return attempt
        }
        error("no snapshot of the self-backup without an active run of A1 in $SNAPSHOT_ATTEMPTS attempts")
    }

    private fun quietSnapshotRun(): Pair<Map<String, String>, Instant> {
        Await.until("a quiet moment in the first half of a minute", Duration.ofMinutes(3)) {
            val second = Instant.now().epochSecond % SECONDS_IN_MINUTE
            second in WINDOW && runs().none { it.finishedAt == null }
        }
        val queuedAt = Instant.now()
        val started = startSelfBackup()
        started.values.forEach { awaitSucceeded(it) }
        return started to queuedAt
    }

    /**
     * A run of A1 whose time touches the self-backup runs: the dump is taken at some moment inside them, and A1, a
     * schedule every minute, may have started a run before that moment (Р40 wants none in the dump, or the catch-up
     * after the restore is skipped as skipped_active). The quiet moment is checked before the runs start, not at the dump.
     */
    private fun overlaps(
        a1: String,
        attempt: Pair<Map<String, String>, Instant>,
    ): Boolean {
        val all = runs()
        val end = all.filter { it.id in attempt.first.values }.maxOf { Instant.parse(checkNotNull(it.finishedAt)) }
        return all.any {
            it.sourceId == a1 &&
                Instant.parse(it.queuedAt) <= end &&
                (it.finishedAt == null || Instant.parse(it.finishedAt) >= attempt.second)
        }
    }

    private fun awaitSucceeded(run: String) {
        check(awaitRun(run) == "succeeded") { "run $run did not succeed: ${api.get("/api/v1/runs/$run").body}" }
    }

    /** The ids of snapshots D (database) and K (keys) the two runs made. */
    private fun selfSnapshots(runs: Map<String, String>): Pair<String, String> {
        fun snapshot(role: String) = api.get("/api/v1/runs/${runs.getValue(role)}").json["steps"].items.single()["backup"]["snapshotId"].text!!
        return snapshot("self_database") to snapshot("self_keys")
    }

    private fun selfSources(): Map<String, String> =
        api.get("/api/v1/self-backup").json["sources"].items.associate { it["role"].text!! to it["sourceId"].text!! }

    /** One more finished run of A1 queued after [queuedAt]: the history after the snapshot. */
    private fun runAfter(
        source: String,
        queuedAt: Instant,
    ): String =
        Await.value("a run of A1 after the snapshot", Duration.ofMinutes(3)) {
            runs()
                .firstOrNull { it.sourceId == source && it.finishedAt != null && it.status == "succeeded" && Instant.parse(it.queuedAt) > queuedAt.plusSeconds(WINDOW_PAD) }
                ?.id
        }

    private fun runs(): List<RunRecord> =
        api.items("/api/v1/runs").map {
            RunRecord(it["id"].text!!, it["sourceId"].text!!, it["trigger"].text!!, it["status"].text!!, it["queuedAt"].text!!, it["finishedAt"].text)
        }

    private fun agents() = api.items("/api/v1/agents")

    private fun sources() = api.items("/api/v1/sources")

    companion object {
        const val SERVER_NAME = "sard.drill.test"
        const val ENDPOINT = "$SERVER_NAME:9090"
        const val BUCKET = "sard-self"
        private const val RUNS_BEFORE = 3
        private const val SECONDS_IN_MINUTE = 60L
        private val WINDOW = 8L..28L
        private const val SNAPSHOT_ATTEMPTS = 5
        private const val WINDOW_PAD = 5L
        private val FINAL = setOf("succeeded", "failed", "cancelled")
    }
}
