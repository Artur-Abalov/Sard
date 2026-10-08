// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.agents.stream.Connection
import dev.sard.server.agents.stream.StreamClients
import dev.sard.server.agents.stream.TestAgent
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.Pem
import dev.sard.server.registration.AgentSnapshot
import dev.sard.server.registration.PluginAction
import dev.sard.server.registration.PluginEntry
import dev.sard.server.registration.Registration
import dev.sard.server.registration.RepositoryEntry
import io.grpc.TlsChannelCredentials
import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import org.bouncycastle.pkcs.jcajce.JcaPKCS10CertificationRequestBuilder
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import tools.jackson.databind.ObjectMapper
import java.io.File
import java.security.KeyPairGenerator
import java.security.spec.ECGenParameterSpec
import java.time.Duration
import java.util.UUID

/** A server on random HTTP and gRPC ports with PostgreSQL, a clock the test moves and a session-based tenant. */
@Target(AnnotationTarget.CLASS)
@Retention(AnnotationRetention.RUNTIME)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    // The periodic stream checks stay out of the way; tests that need one call it by hand.
    properties = ["spring.grpc.server.port=0", "sard.agent.stream.check-interval=1h"],
)
@Import(TestcontainersConfiguration::class, RestApiTestConfiguration::class)
annotation class RestApiTest

/** The plugin config schema of the files plugin, the one the specification's "agent with a snapshot" announces. */
val FILES_SCHEMA: String by lazy { File("../agent/plugins/files/schema.json").readText() }

/** What Register left for an agent: the "agent with a snapshot" of the specification unless told otherwise. */
fun snapshotOf(
    plugins: List<PluginEntry> = listOf(filesPlugin()),
    repositories: List<RepositoryEntry> = listOf(RepositoryEntry("qa", "local", "", "")),
    secretNames: List<String> = listOf("db-password"),
    scriptNames: List<String> = emptyList(),
    version: String = "0.4.0",
    arch: String = "amd64",
) = AgentSnapshot("db1", version, 1, "linux", arch, plugins, repositories, secretNames, scriptNames)

fun filesPlugin(
    name: String = "files",
    schema: String = FILES_SCHEMA,
    actions: List<PluginAction?> = listOf(PluginAction.BACKUP, PluginAction.RESTORE),
) = PluginEntry(name, "0.1.0", schema, actions)

/**
 * The tenants, agents and administrators of one test class, all removed by [close]. Agents are enrolled
 * the way a real one is (token, CSR, Enroll), so their certificates exist.
 */
class RestWorld(
    private val ca: CertificateAuthority,
    private val enrollment: Enrollment,
    private val tokens: EnrollmentTokens,
    private val registration: Registration,
    val jdbc: JdbcTemplate,
    val clock: MovableClock,
    mapper: ObjectMapper,
    httpPort: Int,
    grpcPort: Int,
) : AutoCloseable {
    val api = ApiClient(httpPort, mapper)
    private val clients = StreamClients(ca, enrollment, tokens, jdbc, grpcPort)
    private val tenants = mutableListOf<UUID>()

    init {
        clock.now = T0
    }

    fun tenant(): UUID {
        val id = UUID.randomUUID()
        jdbc.update("insert into tenants (id, name) values (?, ?)", id, "rest-$id")
        tenants += id
        return id
    }

    /** An administrator of a new tenant. */
    fun admin(tenant: UUID = tenant()): ApiSession = api.signIn(tenant)

    private fun newKeys() =
        KeyPairGenerator
            .getInstance("EC")
            .apply {
                initialize(ECGenParameterSpec("secp256r1"))
            }.generateKeyPair()

    private fun csrOf(keys: java.security.KeyPair): ByteArray {
        val signer = JcaContentSignerBuilder("SHA256withECDSA").build(keys.private)
        return JcaPKCS10CertificationRequestBuilder(X500Name("CN=test"), keys.public).build(signer).encoded
    }

    /** A certificate request for [Enrollment.enroll]. */
    fun csr(): ByteArray = csrOf(newKeys())

    /** An agent that enrolled but never sent Register; [credentials] is what it would present over mTLS. */
    fun enroll(
        tenant: UUID,
        hostname: String = "db1",
        builtin: Boolean = false,
    ): TestAgent {
        val keys = newKeys()
        val csr = csrOf(keys)
        val issued = if (builtin) tokens.replaceBuiltin(tenant) else tokens.create(tenant, Duration.ofHours(1))
        val token = issued.reveal()
        val agent = enrollment.enroll(token, csr, hostname)
        val credentials =
            TlsChannelCredentials
                .newBuilder()
                .trustManager(ca.caBundlePem().byteInputStream())
                .keyManager(agent.certificateChainPem.byteInputStream(), Pem.privateKey(keys.private).byteInputStream())
                .build()
        return TestAgent(tenant, agent.agentId, credentials)
    }

    /** An enrolled agent whose last Register is [snapshot]. */
    fun agent(
        tenant: UUID,
        snapshot: AgentSnapshot? = snapshotOf(),
        hostname: String = "db1",
        builtin: Boolean = false,
    ): TestAgent {
        val agent = enroll(tenant, hostname, builtin)
        snapshot?.let { registration.register(tenant, agent.agentId, it.copy(hostname = hostname)) }
        return agent
    }

    fun register(
        agent: TestAgent,
        snapshot: AgentSnapshot,
    ) = registration.register(agent.tenantId, agent.agentId, snapshot)

    fun connect(agent: TestAgent): Connection = clients.connect(agent)

    /**
     * Puts the run and its steps into [status] as the dispatcher and the result receiver would leave them,
     * at [at]; `lost`, `failed`, `rejected` and `timed_out` finish them with [message].
     */
    fun forceRun(
        runId: UUID,
        status: String,
        message: String? = null,
        at: java.time.Instant = clock.now,
    ) {
        val time = java.sql.Timestamp.from(at)
        val dispatched = if (status == "queued") null else time
        val started = if (status in setOf("queued", "dispatched")) null else time
        val finished = if (status in setOf("queued", "dispatched", "running")) null else time
        jdbc.update(
            "update run_steps set status = ?, message = ?, dispatched_at = ?, started_at = ?, finished_at = ? " +
                "where run_id = ?",
            status,
            message,
            dispatched,
            started,
            finished,
            runId,
        )
        val runStatus = if (status in setOf("lost", "rejected", "timed_out")) "failed" else status
        jdbc.update(
            "update runs set status = ?, message = ?, started_at = ?, finished_at = ? where id = ?",
            runStatus,
            message,
            started,
            finished,
            runId,
        )
    }

    /** [count] log lines "line N" for the step, numbered from [from], as S7a stores them. */
    fun insertLogs(
        step: UUID,
        tenant: UUID,
        count: Int,
        from: Int = 1,
    ) {
        val at = java.sql.Timestamp.from(clock.now)
        for (seq in from until from + count) {
            jdbc.update(
                "insert into step_logs (tenant_id, step_id, seq, received_at, time, level, text) " +
                    "values (?, ?, ?, ?, ?, 'info', ?)",
                tenant,
                step,
                seq.toLong(),
                at,
                at,
                "line $seq",
            )
        }
        jdbc.update("update run_steps set log_lines = log_lines + ? where id = ?", count.toLong(), step)
    }

    /** A snapshot of the step (its run's source) as S7a would have recorded it, at [at]. */
    fun insertSnapshot(
        tenant: UUID,
        step: UUID,
        name: String,
        at: java.time.Instant = clock.now,
    ) {
        jdbc.update(
            "insert into snapshots (id, tenant_id, source_id, step_id, agent_id, repository_name, repository_id, " +
                "snapshot_id, total_bytes, added_bytes, created_at) " +
                "select ?, tenant_id, source_id, id, agent_id, 'qa', ?, ?, 1000, 10, ? " +
                "from run_steps where id = ? and tenant_id = ?",
            UUID.randomUUID(),
            "r".repeat(64),
            name,
            java.sql.Timestamp.from(at),
            step,
            tenant,
        )
    }

    fun count(
        table: String,
        tenant: UUID,
    ): Int = jdbc.queryForObject("select count(*) from $table where tenant_id = ?", Int::class.java, tenant) ?: 0

    override fun close() {
        clients.closeChannels()
        for (tenant in tenants) {
            for (table in TENANT_TABLES) jdbc.update("delete from $table where tenant_id = ?", tenant)
            jdbc.update("delete from tenants where id = ?", tenant)
        }
    }

    private companion object {
        /** Children first. */
        val TENANT_TABLES =
            listOf(
                "snapshots",
                "step_logs",
                "run_steps",
                "runs",
                "sources",
                "agent_plugins",
                "agent_repositories",
                "agent_certificates",
                "enrollment_tokens",
                "agents",
            )
    }
}
