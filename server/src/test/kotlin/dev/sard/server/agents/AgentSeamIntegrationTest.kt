// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.Pem
import org.junit.jupiter.api.io.TempDir
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.nio.file.Files
import java.nio.file.Path
import java.time.Duration
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals

private val REGISTER_WAIT: Duration = Duration.ofSeconds(30)
private const val POLL_MILLIS = 100L

/** The binary Gradle's buildTestAgent task built once for every test of this run. */
private fun agentBinary(): Path {
    val path = checkNotNull(System.getProperty("sard.test.agent-binary")) { "run through Gradle's buildTestAgent" }
    return Path.of(path).also { check(Files.isExecutable(it)) { "no agent binary at $it" } }
}

/**
 * S4a, verification strategy 7: the real Go sard-agent (A3), holding a certificate from
 * Enroll, registers against the server built from this code. Nothing is faked on the agent
 * side: its own config loader, plugin registry and transport build the request.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class AgentSeamIntegrationTest(
    @Autowired private val ca: CertificateAuthority,
    @Autowired enrollment: Enrollment,
    @Autowired tokens: EnrollmentTokens,
    @Autowired private val jdbc: JdbcTemplate,
    @LocalGrpcServerPort private val grpcPort: Int,
) {
    @TempDir
    lateinit var dir: Path

    private val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)
    private var process: Process? = null

    @AfterTest
    fun cleanUp() {
        process?.let {
            it.destroy()
            if (!it.waitFor(10, TimeUnit.SECONDS)) it.destroyForcibly()
        }
        fixtures.close()
    }

    /** The agent's YAML: its enrolled certificate, one local repository, one secret, one script. */
    private fun config(agent: EnrolledAgent): Path {
        val write = { name: String, text: String -> dir.resolve(name).also { Files.writeString(it, text) } }
        val caFile = write("ca.pem", ca.caBundlePem())
        val cert = write("agent.pem", agent.chainPem)
        val key = write("agent.key", Pem.privateKey(agent.keys.private))
        val password = write("repo.password", "not-a-real-key")
        // restic is deliberately absent: the agent then announces the repository with an empty id.
        val yaml =
            """
            server:
              address: localhost:$grpcPort
            tls:
              ca_file: $caFile
              cert_file: $cert
              key_file: $key
            restic:
              path: ${dir.resolve("no-restic")}
            executor:
              state_dir: ${dir.resolve("state")}
            repositories:
              - name: main
                url: ${dir.resolve("repo")}
                password_file: $password
            secrets:
              pg-prod: ${dir.resolve("pg-prod.secret")}
            scripts:
              pre-dump: /bin/true
            """.trimIndent()
        return write("agent.yaml", yaml)
    }

    private fun agentLog(): String = Files.readString(dir.resolve("agent.log"))

    private fun awaitRegistered(agent: EnrolledAgent) {
        val sql = "select count(*) from agents where id = ? and last_register_at is not null"
        val deadline = System.nanoTime() + REGISTER_WAIT.toNanos()
        while (jdbc.queryForObject(sql, Int::class.java, agent.identity.agentId) == 0) {
            check(process!!.isAlive) { "the agent exited:\n${agentLog()}" }
            check(System.nanoTime() < deadline) { "no Register within $REGISTER_WAIT:\n${agentLog()}" }
            Thread.sleep(POLL_MILLIS)
        }
    }

    @Test
    fun `the real agent registers its snapshot with a certificate from Enroll`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        process =
            ProcessBuilder(agentBinary().toString(), "--config", config(agent).toString())
                .redirectErrorStream(true)
                .redirectOutput(dir.resolve("agent.log").toFile())
                .start()
        awaitRegistered(agent)

        val id = agent.identity.agentId
        val row =
            jdbc.queryForMap(
                "select hostname, agent_version, os, arch, protocol_version, " +
                    "array_to_string(secret_names, ',') secrets, array_to_string(script_names, ',') scripts " +
                    "from agents where id = ?",
                id,
            )
        val expectedRow =
            mapOf(
                "hostname" to Files.readString(Path.of("/proc/sys/kernel/hostname")).trim(),
                "agent_version" to "seam-test",
                "os" to "linux",
                "arch" to goArch(),
                "protocol_version" to 1,
                "secrets" to "pg-prod",
                "scripts" to "pre-dump",
            )
        assertEquals(expectedRow, row)

        val plugins =
            jdbc.queryForList(
                "select name, version, array_to_string(actions, ','), jsonb_typeof(config_schema), tenant_id " +
                    "from agent_plugins where agent_id = ? order by name",
                id,
            )
        val tenant = agent.identity.tenantId
        val expectedPlugins =
            listOf("files", "mysql", "network", "postgresql").map {
                listOf(it, "seam-test", "backup,restore,verify", "object", tenant)
            }
        assertEquals(expectedPlugins, plugins.map { it.values.toList() })

        val repositories =
            jdbc.queryForList(
                "select name, backend, repository_id, crypto_provider, tenant_id " +
                    "from agent_repositories where agent_id = ?",
                id,
            )
        assertEquals(listOf(listOf("main", "local", null, null, tenant)), repositories.map { it.values.toList() })
    }

    /** GOARCH of this machine, as the agent reports runtime.GOARCH. */
    private fun goArch(): String =
        when (val arch = System.getProperty("os.arch")) {
            "amd64", "x86_64" -> "amd64"
            "aarch64" -> "arm64"
            else -> arch
        }
}
