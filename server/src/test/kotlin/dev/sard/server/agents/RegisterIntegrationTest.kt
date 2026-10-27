// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.protobuf.ByteString
import com.google.protobuf.UnknownFieldSet
import com.google.rpc.ErrorInfo
import dev.sard.proto.agent.v1.Action
import dev.sard.proto.agent.v1.AgentServiceGrpc
import dev.sard.proto.agent.v1.Plugin
import dev.sard.proto.agent.v1.RegisterRequest
import dev.sard.proto.agent.v1.RegisterResponse
import dev.sard.proto.agent.v1.RepositoryInfo
import dev.sard.proto.agent.v1.Sandbox
import dev.sard.proto.agent.v1.SandboxImage
import dev.sard.proto.agent.v1.SecretInfo
import dev.sard.proto.agent.v1.VerifierStatus
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.api.MACHINE_ID
import dev.sard.server.api.OTHER_MACHINE_ID
import dev.sard.server.api.captureLogs
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import java.time.temporal.ChronoUnit
import java.util.UUID
import java.util.concurrent.CompletableFuture
import java.util.concurrent.TimeUnit
import javax.sql.DataSource
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

private const val DEADLINE_SECONDS = 10L
private const val POLL_MILLIS = 20L
private val HEX_A = "a".repeat(64)
private val HEX_B = "b".repeat(64)
private const val MARKER_MID = "MARKER-MID-0001"
private const val FILES_SCHEMA = """{"type":"object","properties":{"path":{"type":"string"}}}"""
private const val RESTORE_SCHEMA = """{"type":"object","properties":{"destination":{"type":"string"}}}"""

/** The server's clock in this test: fixed, so last_register_at is known exactly. */
private val NOW: Instant = Instant.now().truncatedTo(ChronoUnit.MICROS)

@TestConfiguration(proxyBeanMethods = false)
class FixedClockConfiguration {
    @Bean
    fun clock(): Clock = Clock.fixed(NOW, ZoneOffset.UTC)
}

/** Everything Register stores for one agent, read back with plain SQL. */
private data class Stored(
    val agent: Map<String, Any?>,
    val plugins: Set<List<Any?>>,
    val repositories: Set<List<Any?>>,
)

private fun plugin(
    name: String,
    version: String = "0.1.0",
    schema: String = """{"type": "object"}""",
    vararg actions: Action = arrayOf(Action.ACTION_BACKUP, Action.ACTION_RESTORE),
): Plugin =
    Plugin
        .newBuilder()
        .setName(name)
        .setVersion(version)
        .setConfigSchema(schema)
        .addAllActions(actions.toList())
        .build()

private fun repository(
    name: String,
    backend: String = "s3",
    id: String = "",
    crypto: String = "",
): RepositoryInfo =
    RepositoryInfo
        .newBuilder()
        .setName(name)
        .setBackend(backend)
        .setRepositoryId(id)
        .setCryptoProvider(crypto)
        .build()

/** Snapshot A: what a first Register announces. */
private fun snapshotA(): RegisterRequest.Builder =
    RegisterRequest
        .newBuilder()
        .setProtocolVersion(1)
        .setHostname("db1")
        .setAgentVersion("0.1.0")
        .setOs("linux")
        .setArch("amd64")
        .addPlugins(plugin("postgresql"))
        .addPlugins(plugin("files", schema = FILES_SCHEMA))
        .addRepositories(repository("main", id = HEX_A))
        .addRepositories(repository("offsite", backend = "sftp"))
        .addAllSecretNames(listOf("pg-prod", "PG_PASSWORD"))
        .addAllScriptNames(listOf("pre-dump"))
        .setMachineId(MACHINE_ID)

/** Snapshot B: differs from A in every field Register stores. */
private fun snapshotB(): RegisterRequest.Builder =
    RegisterRequest
        .newBuilder()
        .setProtocolVersion(1)
        .setHostname("db1-renamed")
        .setAgentVersion("0.2.0")
        .setOs("freebsd")
        .setArch("arm64")
        .addPlugins(plugin("postgresql", version = "0.2.0", schema = "true", Action.ACTION_VERIFY))
        .addPlugins(plugin("mysql"))
        .addRepositories(repository("main", backend = "rest", id = HEX_B, crypto = "gost"))
        .addAllSecretNames(listOf("mysql-root"))
        .setMachineId(OTHER_MACHINE_ID)

/**
 * S4a, verification strategy 1-6: Register replaces the agent's snapshot atomically and only
 * in the agent's own tenant; refusals change nothing.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0", "sard.agent.heartbeat-interval=17s"],
)
@Import(TestcontainersConfiguration::class, FixedClockConfiguration::class)
class RegisterIntegrationTest(
    @Autowired ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val dataSource: DataSource,
    @LocalGrpcServerPort grpcPort: Int,
) {
    private val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)

    @AfterTest
    fun cleanUp() = fixtures.close()

    private fun register(
        agent: EnrolledAgent,
        request: RegisterRequest.Builder,
    ): RegisterResponse =
        AgentServiceGrpc
            .newBlockingStub(fixtures.channel(agent))
            .withDeadlineAfter(DEADLINE_SECONDS, TimeUnit.SECONDS)
            .register(request.build())

    private fun refusal(
        agent: EnrolledAgent,
        request: RegisterRequest.Builder,
    ): Pair<Status.Code, ErrorInfo> {
        val error = assertFailsWith<StatusRuntimeException> { register(agent, request) }
        val details = checkNotNull(StatusProto.fromThrowable(error)).detailsList
        assertEquals(1, details.size)
        return error.status.code to details.single().unpack(ErrorInfo::class.java)
    }

    private fun stored(agent: EnrolledAgent): Stored {
        val id = agent.identity.agentId
        val columns =
            "tenant_id, hostname, agent_version, os, arch, protocol_version, " +
                "array_to_string(secret_names, ',') secrets, array_to_string(script_names, ',') scripts, " +
                "last_register_at, machine_id"
        val row = jdbc.queryForMap("select $columns from agents where id = ?", id)
        val plugins =
            jdbc.queryForList(
                "select tenant_id, name, version, config_schema::text, array_to_string(actions, ',') " +
                    "from agent_plugins where agent_id = ?",
                id,
            )
        val repositories =
            jdbc.queryForList(
                "select tenant_id, name, backend, repository_id, crypto_provider " +
                    "from agent_repositories where agent_id = ?",
                id,
            )
        return Stored(row, plugins.map { it.values.toList() }.toSet(), repositories.map { it.values.toList() }.toSet())
    }

    /** jsonb's own text form of [json], so a stored schema compares whatever whitespace the agent sent. */
    private fun jsonb(json: String): String = jdbc.queryForObject("select ?::jsonb::text", String::class.java, json)!!

    private fun expectedA(agent: EnrolledAgent): Stored {
        val tenant = agent.identity.tenantId
        return Stored(
            linkedMapOf(
                "tenant_id" to tenant,
                "hostname" to "db1",
                "agent_version" to "0.1.0",
                "os" to "linux",
                "arch" to "amd64",
                "protocol_version" to 1,
                "secrets" to "pg-prod,PG_PASSWORD",
                "scripts" to "pre-dump",
                "last_register_at" to Timestamp.from(NOW),
                "machine_id" to MACHINE_ID,
            ),
            setOf(
                listOf(tenant, "postgresql", "0.1.0", jsonb("""{"type": "object"}"""), "backup,restore"),
                listOf(tenant, "files", "0.1.0", jsonb(FILES_SCHEMA), "backup,restore"),
            ),
            setOf(listOf(tenant, "main", "s3", HEX_A, null), listOf(tenant, "offsite", "sftp", null, null)),
        )
    }

    private fun expectedB(agent: EnrolledAgent): Stored {
        val tenant = agent.identity.tenantId
        return Stored(
            linkedMapOf(
                "tenant_id" to tenant,
                "hostname" to "db1-renamed",
                "agent_version" to "0.2.0",
                "os" to "freebsd",
                "arch" to "arm64",
                "protocol_version" to 1,
                "secrets" to "mysql-root",
                "scripts" to "",
                "last_register_at" to Timestamp.from(NOW),
                "machine_id" to OTHER_MACHINE_ID,
            ),
            setOf(
                listOf(tenant, "postgresql", "0.2.0", "true", "verify"),
                listOf(tenant, "mysql", "0.1.0", jsonb("""{"type": "object"}"""), "backup,restore"),
            ),
            setOf(listOf(tenant, "main", "rest", HEX_B, "gost")),
        )
    }

    // --- 1: first Register stores everything, the next one replaces the sets whole

    @Test
    fun `the first Register stores the whole snapshot`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        register(agent, snapshotA())
        assertEquals(expectedA(agent), stored(agent))
    }

    @Test
    fun `a later Register replaces the snapshot whole, dropping what it no longer lists`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        register(agent, snapshotA())
        register(agent, snapshotB())
        assertEquals(expectedB(agent), stored(agent))
    }

    @Test
    fun `an empty snapshot empties every set`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        register(agent, snapshotA())
        val empty =
            snapshotA()
                .clearPlugins()
                .clearRepositories()
                .clearSecretNames()
                .clearScriptNames()
        register(agent, empty)
        val stored = stored(agent)
        val sets = listOf(stored.plugins, stored.repositories, stored.agent["secrets"], stored.agent["scripts"])
        assertEquals(listOf(emptySet<Any>(), emptySet<Any>(), "", ""), sets)
    }

    // --- R2a: the restore directory and the restore schema

    private fun restoreOf(agent: EnrolledAgent): Pair<String?, String?> =
        jdbc.queryForObject(
            "select restore_dir from agents where id = ?",
            String::class.java,
            agent.identity.agentId,
        ) to
            jdbc
                .queryForList(
                    "select restore_config_schema::text from agent_plugins where agent_id = ? and name = 'files'",
                    String::class.java,
                    agent.identity.agentId,
                ).singleOrNull()

    @Test
    fun `Register stores the binding of every secret, and a later one replaces them (S11)`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val bound =
            SecretInfo
                .newBuilder()
                .setName("pg-prod")
                .setPlugin("postgresql")
                .addAllTargets(listOf("db.example.com:5432/app", "[::1]:5432/*"))
                .addAuth("md5")
                .setAllowTls("require")
                .setAllowNoChannelBinding(true)
        val unbound = SecretInfo.newBuilder().setName("PG_PASSWORD").setBindingProblem("no binding")
        register(agent, snapshotA().addSecrets(bound).addSecrets(unbound))
        val sql = "select secret_bindings from agents where id = ?"
        val stored = { jdbc.queryForObject(sql, String::class.java, agent.identity.agentId) }
        assertEquals(
            jsonb(
                """[{"name":"pg-prod","plugin":"postgresql","targets":["db.example.com:5432/app","[::1]:5432/*"],""" +
                    """"auth":["md5"],"allow_tls":"require","allow_no_channel_binding":true,"problem":""},""" +
                    """{"name":"PG_PASSWORD","plugin":"","targets":[],"auth":[],"allow_tls":"",""" +
                    """"allow_no_channel_binding":false,"problem":"no binding"}]""",
            ),
            stored(),
        )
        register(agent, snapshotA())
        assertEquals("[]", stored())
    }

    @Test
    fun `Register stores the restore directory and the restore schema of a plugin`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val files = plugin("files", schema = FILES_SCHEMA).toBuilder().setRestoreConfigSchema(RESTORE_SCHEMA).build()
        register(agent, snapshotA().clearPlugins().addPlugins(files).setRestoreDir("/var/lib/sard-agent/restore"))
        assertEquals("/var/lib/sard-agent/restore" to jsonb(RESTORE_SCHEMA), restoreOf(agent))
    }

    @Test
    fun `a later Register without them leaves neither`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val files = plugin("files", schema = FILES_SCHEMA).toBuilder().setRestoreConfigSchema(RESTORE_SCHEMA).build()
        register(agent, snapshotA().clearPlugins().addPlugins(files).setRestoreDir("/srv/restore"))
        register(agent, snapshotA())
        assertEquals(null to null, restoreOf(agent))
    }

    @Test
    fun `a restore directory that is not a path is INVALID_ARGUMENT and changes nothing`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        register(agent, snapshotA().setRestoreDir("/srv/restore"))
        val (code, info) = refusal(agent, snapshotB().setRestoreDir("relative"))
        assertEquals(Status.Code.INVALID_ARGUMENT, code)
        assertEquals("restore_dir", info.metadataMap["field"])
        assertEquals("/srv/restore", restoreOf(agent).first)
    }

    // --- 2: two concurrent Registers of one agent never mix

    @Test
    fun `two concurrent Registers of one agent leave exactly one of the two snapshots`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val first: CompletableFuture<RegisterResponse>
        val second: CompletableFuture<RegisterResponse>
        dataSource.connection.use { blocker ->
            // The test holds the agent's row lock, so both Registers park on it before writing anything.
            blocker.autoCommit = false
            blocker.prepareStatement("select id from agents where id = ? for update").use {
                it.setObject(1, agent.identity.agentId)
                it.executeQuery().close()
            }
            first = CompletableFuture.supplyAsync { register(agent, snapshotA()) }
            second = CompletableFuture.supplyAsync { register(agent, snapshotB()) }
            awaitLockWaiters(2)
            blocker.commit()
        }
        first.get(DEADLINE_SECONDS, TimeUnit.SECONDS)
        second.get(DEADLINE_SECONDS, TimeUnit.SECONDS)
        val stored = stored(agent)
        assertTrue(stored == expectedA(agent) || stored == expectedB(agent), "mixed snapshot: $stored")
    }

    private fun awaitLockWaiters(count: Int) {
        val sql = "select count(*) from pg_stat_activity where wait_event_type = 'Lock' and query like '%agents%'"
        val deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(DEADLINE_SECONDS)
        while (jdbc.queryForObject(sql, Int::class.java)!! < count) {
            check(System.nanoTime() < deadline) { "the Registers never waited for the agent's row lock" }
            Thread.sleep(POLL_MILLIS)
        }
    }

    // --- 3: an unsupported protocol

    @Test
    fun `an unsupported protocol is FAILED_PRECONDITION with the range and changes nothing`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        register(agent, snapshotA())
        for (version in listOf(0, 2, -1)) {
            val (code, info) = refusal(agent, snapshotB().setProtocolVersion(version))
            assertEquals(Status.Code.FAILED_PRECONDITION, code)
            assertEquals("PROTOCOL_UNSUPPORTED", info.reason)
            assertEquals(mapOf("min_supported" to "1", "max_supported" to "1"), info.metadataMap)
        }
        assertEquals(expectedA(agent), stored(agent))
    }

    // --- 4: limits

    @Test
    fun `a snapshot over a limit is INVALID_ARGUMENT with its reason and changes nothing`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        register(agent, snapshotA())
        val cases =
            listOf(
                snapshotB().addAllPlugins((0..64).map { plugin("p$it") }) to
                    ("SNAPSHOT_TOO_LARGE" to mapOf("field" to "plugins", "limit" to "64")),
                snapshotB().addSecretNames("pg prod") to ("NAME_INVALID" to mapOf("field" to "secret_names[1]")),
                snapshotB().addPlugins(plugin("mysql")) to ("NAME_DUPLICATE" to mapOf("field" to "plugins[2].name")),
                snapshotB().addPlugins(plugin("bad", schema = "{")) to
                    ("CONFIG_SCHEMA_INVALID" to mapOf("field" to "plugins[2].config_schema")),
                snapshotB().addPlugins(plugin("unknown", actions = arrayOf(Action.ACTION_UNSPECIFIED))) to
                    ("FIELD_INVALID" to mapOf("field" to "plugins[2].actions")),
                snapshotB().setHostname("") to ("HOSTNAME_INVALID" to mapOf("field" to "hostname")),
                // PostgreSQL cannot store NUL: without the rule this was a database error, UNAVAILABLE.
                snapshotB().setHostname("db1\u0000") to ("HOSTNAME_INVALID" to mapOf("field" to "hostname")),
            )
        for ((request, expected) in cases) {
            val (code, info) = refusal(agent, request)
            assertEquals(Status.Code.INVALID_ARGUMENT to expected, code to (info.reason to info.metadataMap))
        }
        assertEquals(expectedA(agent), stored(agent))
    }

    // --- 5: tenants

    @Test
    fun `a Register writes only its own agent, stamped with the agent's tenant`() {
        val acme = fixtures.enrolled(fixtures.tenant())
        val globex = fixtures.enrolled(fixtures.tenant())
        val acmeNeighbour = fixtures.enrolled(acme.identity.tenantId)
        register(globex, snapshotB())
        register(acmeNeighbour, snapshotB())
        register(acme, snapshotA())
        register(acme, snapshotA().clearPlugins().clearRepositories())
        assertEquals(expectedB(globex), stored(globex))
        assertEquals(expectedB(acmeNeighbour), stored(acmeNeighbour))
    }

    // --- 6: the answer

    @Test
    fun `the answer carries the agent id of the certificate and the configured heartbeat interval`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val response = register(agent, snapshotA())
        assertEquals(agent.identity.agentId, UUID.fromString(response.agentId))
        assertEquals(17L to 0, response.heartbeatInterval.seconds to response.heartbeatInterval.nanos)
    }

    // --- V4: verifier status

    private fun verifierOf(agent: EnrolledAgent): List<Any?> =
        jdbc
            .queryForList(
                "select verifier_reported, verify_free_bytes, verify_sandbox from agents where id = ?",
                agent.identity.agentId,
            ).single()
            .values
            .toList()

    @Test
    fun `a Register stores the verifier status and the next one without it forgets it`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val status = VerifierStatus.newBuilder().setWorkFreeBytes(1_000).setSandbox(Sandbox.SANDBOX_PODMAN)

        register(agent, snapshotA().setVerifier(status))
        assertEquals(listOf<Any?>(true, 1_000L, "podman"), verifierOf(agent))

        register(agent, snapshotA())
        assertEquals(listOf<Any?>(false, null, null), verifierOf(agent))
    }

    @Test
    fun `an empty verifier status is reported with free space and sandbox unknown`() {
        val agent = fixtures.enrolled(fixtures.tenant())

        register(agent, snapshotA().setVerifier(VerifierStatus.getDefaultInstance()))

        assertEquals(listOf<Any?>(true, null, null), verifierOf(agent))
    }

    @Test
    fun `a Register stores the sandbox version and the images, the next one without them forgets them (V5)`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val image =
            SandboxImage
                .newBuilder()
                .setName("postgres")
                .setVersion("17.6")
                .setDigest("sha256:ab")
        val status =
            VerifierStatus
                .newBuilder()
                .setSandbox(Sandbox.SANDBOX_DOCKER)
                .setSandboxVersion("29.9.2")
                .addImages(image)
        val query = "select verify_sandbox_version, verify_images::text from agents where id = ?"
        val stored = {
            jdbc
                .queryForList(query, agent.identity.agentId)
                .single()
                .values
                .toList()
        }

        register(agent, snapshotA().setVerifier(status))
        val images = """[{"name": "postgres", "digest": "sha256:ab", "version": "17.6"}]"""
        assertEquals(listOf<Any?>("29.9.2", images), stored())

        register(agent, snapshotA())
        assertEquals(listOf<Any?>(null, "[]"), stored())
    }

    // --- V1: machine_id

    private fun machineIdOf(agent: EnrolledAgent): String? =
        jdbc.queryForObject("select machine_id from agents where id = ?", String::class.java, agent.identity.agentId)

    @Test
    fun `machine_id из Register сохраняется у агента`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        assertEquals(null, machineIdOf(agent))
        register(agent, snapshotA())
        assertEquals(MACHINE_ID, machineIdOf(agent))
    }

    @Test
    fun `Следующий Register перезаписывает machine_id`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        register(agent, snapshotA())
        register(agent, snapshotA().setMachineId(OTHER_MACHINE_ID))
        assertEquals(OTHER_MACHINE_ID, machineIdOf(agent))
    }

    @Test
    fun `Недопустимый machine_id отклоняет Register как FIELD_INVALID`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        register(agent, snapshotA())
        val bad =
            listOf(
                "",
                "a".repeat(63),
                "a".repeat(65),
                "A".repeat(64),
                "a".repeat(63) + "g",
            )
        for (value in bad) {
            val (code, info) = refusal(agent, snapshotB().setHostname("db2").setMachineId(value))
            assertEquals(Status.Code.INVALID_ARGUMENT, code, value)
            assertEquals("FIELD_INVALID", info.reason, value)
            assertEquals(mapOf("field" to "machine_id"), info.metadataMap, value)
            assertEquals(expectedA(agent), stored(agent), value)
        }
    }

    @Test
    fun `Ошибка hostname важнее пустого machine_id`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val (code, info) = refusal(agent, snapshotA().setHostname("").setMachineId(""))
        assertEquals(Status.Code.INVALID_ARGUMENT to "HOSTNAME_INVALID", code to info.reason)
    }

    @Test
    fun `Отказ по machine_id не раскрывает значение в тексте статуса и в логе`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        lateinit var error: StatusRuntimeException
        val logs = captureLogs { error = assertFailsWith { register(agent, snapshotA().setMachineId(MARKER_MID)) } }

        val info =
            StatusProto
                .fromThrowable(error)!!
                .detailsList
                .single()
                .unpack(ErrorInfo::class.java)
        assertEquals("FIELD_INVALID", info.reason)
        assertTrue(MARKER_MID !in error.status.description.orEmpty())
        assertTrue(MARKER_MID !in info.metadataMap.values.toString())
        assertTrue(logs.none { MARKER_MID in it }, logs.toString())
    }

    @Test
    fun `Успешный Register не пишет machine_id в лог`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val logs = captureLogs { register(agent, snapshotA()) }
        assertTrue(logs.none { MACHINE_ID in it }, logs.toString())
    }

    @Test
    fun `Неизвестное поле в Register не меняет роль агента`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val verifier =
            UnknownFieldSet.Field
                .newBuilder()
                .addLengthDelimited(ByteString.copyFromUtf8("verifier"))
                .build()
        val request = snapshotA().setUnknownFields(UnknownFieldSet.newBuilder().addField(99, verifier).build())

        register(agent, request)

        val sql = "select role from agents where id = ?"
        val role = jdbc.queryForObject(sql, String::class.java, agent.identity.agentId)
        assertEquals("source", role)
    }
}
