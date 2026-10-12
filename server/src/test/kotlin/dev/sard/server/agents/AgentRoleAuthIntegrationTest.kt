// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.rpc.ErrorInfo
import dev.sard.proto.agent.v1.AgentServiceGrpc
import dev.sard.proto.agent.v1.RegisterResponse
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.api.captureLogs
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.AgentRole
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.ForgedRole
import io.grpc.CallOptions
import io.grpc.Metadata
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto
import io.grpc.stub.ClientCalls
import org.hibernate.Session
import org.hibernate.exception.JDBCConnectionException
import org.mockito.ArgumentMatchers
import org.mockito.Mockito
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import org.springframework.test.context.bean.override.mockito.MockitoSpyBean
import java.sql.SQLException
import java.util.UUID
import java.util.concurrent.TimeUnit
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

private const val DEADLINE_SECONDS = 10L
private const val MARKER = "marker-ROLE-7f"
private val ROLE_REASONS = setOf("CERT_ROLE_MISSING", "CERT_ROLE_INVALID", "CERT_IDENTITY_MISMATCH")

/** What a refused call says: the status code and the single ErrorInfo reason. */
private data class Refusal(
    val code: Status.Code,
    val reason: String?,
)

/** V1, rule "Сертификат без допустимой роли или с чужой ролью отклоняется S3". */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class AgentRoleAuthIntegrationTest(
    @Autowired private val ca: CertificateAuthority,
    @Autowired private val enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val jdbc: JdbcTemplate,
    @LocalGrpcServerPort grpcPort: Int,
) {
    @MockitoSpyBean
    lateinit var sessions: TenantSessions

    private val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)
    private val tenant = fixtures.tenant()

    @AfterTest
    fun cleanUp() {
        Mockito.reset(sessions)
        fixtures.close()
    }

    private fun register(agent: EnrolledAgent): RegisterResponse =
        AgentServiceGrpc
            .newBlockingStub(fixtures.channel(agent))
            .withDeadlineAfter(DEADLINE_SECONDS, TimeUnit.SECONDS)
            .register(fixtures.registerRequest().build())

    private fun refusalOf(block: () -> Unit): Refusal {
        val error = assertFailsWith<StatusRuntimeException> { block() }
        val details = StatusProto.fromStatusAndTrailers(error.status, error.trailers ?: Metadata()).detailsList
        return Refusal(error.status.code, details.singleOrNull()?.unpack(ErrorInfo::class.java)?.reason)
    }

    /** A Mockito matcher Kotlin accepts for a non-null parameter: [placeholder] is never used. */
    private fun <T : Any> anything(placeholder: T): T {
        ArgumentMatchers.any<Any>()
        return placeholder
    }

    private fun refused(reason: String) = Refusal(Status.Code.UNAUTHENTICATED, reason)

    private fun snapshotOf(agent: EnrolledAgent) =
        jdbc.queryForObject("select a::text from agents a where id = ?", String::class.java, agent.identity.agentId)

    @Test
    fun `Сертификат без роли или с непонятной ролью получает отказ S3`() {
        val missing = listOf("sard://role/source", "https://roles/source")
        val invalid =
            listOf(
                "sard://roles/admin",
                "sard://roles/Source",
                "SARD://ROLES/source",
                "sard://roles/",
                "sard://roles",
                "sard://roles/source/",
                "sard://roles/source?x=1",
                "sard://roles/source#x",
                "sard://u@roles/source",
                "sard://roles:443/source",
            )
        val cases =
            listOf(ForgedRole.Absent to "CERT_ROLE_MISSING") +
                missing.map { ForgedRole.Uris(listOf(it)) to "CERT_ROLE_MISSING" } +
                invalid.map { ForgedRole.Uris(listOf(it)) to "CERT_ROLE_INVALID" } +
                listOf(
                    ForgedRole.Uris(listOf("sard://roles/source", "sard://roles/source")) to "CERT_ROLE_INVALID",
                    ForgedRole.Uris(listOf("sard://roles/source", "sard://roles/verifier")) to "CERT_ROLE_INVALID",
                )
        for ((role, reason) in cases) {
            val agent = fixtures.enrolled(tenant)
            val before = snapshotOf(agent)
            val forged = fixtures.forged(agent, role)
            assertEquals(refused(reason), refusalOf { register(forged) }, "$role")
            assertEquals(before, snapshotOf(agent), "$role")
        }
    }

    @Test
    fun `Порядок имён в SAN не влияет на проверку S3`() {
        for (role in AgentRole.entries) {
            val agent = fixtures.enrolled(tenant, role)
            val forged = fixtures.forged(agent, ForgedRole.Uris(listOf("sard://roles/${role.stored}"), first = true))
            val response =
                AgentServiceGrpc
                    .newBlockingStub(fixtures.channel(forged))
                    .withDeadlineAfter(DEADLINE_SECONDS, TimeUnit.SECONDS)
                    .register(fixtures.registerRequest().setHostname("db9").build())
            assertEquals(agent.identity.agentId.toString(), response.agentId)
            val sql = "select hostname from agents where id = ?"
            val host = jdbc.queryForObject(sql, String::class.java, agent.identity.agentId)
            assertEquals("db9", host)
        }
    }

    @Test
    fun `Роль сертификата, отличная от роли агента в базе, отклоняется как несовпадение личности`() {
        val source = fixtures.enrolled(tenant)
        val asVerifier = fixtures.forged(source, ForgedRole.Text("verifier"))
        assertEquals(refused("CERT_IDENTITY_MISMATCH"), refusalOf { register(asVerifier) })
    }

    @Test
    fun `Верификатор с сертификатом роли source отклоняется как несовпадение личности`() {
        val verifier = fixtures.enrolled(tenant, AgentRole.VERIFIER)
        val asSource = fixtures.forged(verifier, ForgedRole.Text("source"))
        assertEquals(refused("CERT_IDENTITY_MISMATCH"), refusalOf { register(asSource) })
    }

    @Test
    fun `Каждый метод AgentService проверяет роль сертификата`() {
        val agent = fixtures.enrolled(tenant)
        val forged = fixtures.forged(agent, ForgedRole.Absent)
        val methods = AgentServiceGrpc.getServiceDescriptor().methods.map { it.fullMethodName }
        assertTrue(methods.size >= 3)
        for (method in methods) {
            val options = CallOptions.DEFAULT.withDeadlineAfter(DEADLINE_SECONDS, TimeUnit.SECONDS)
            val outcome =
                refusalOf {
                    ClientCalls.blockingUnaryCall(fixtures.channel(forged), rawMethod(method), options, ByteArray(0))
                }
            assertEquals(refused("CERT_ROLE_MISSING"), outcome, method)
        }
    }

    @Test
    fun `Расхождение SAN важнее отсутствия роли`() {
        val agent = fixtures.enrolled(tenant)
        val other = AgentIdentity(tenant, UUID.randomUUID())
        val forged = fixtures.forged(agent, ForgedRole.Absent, identity = other)
        assertEquals(refused("CERT_IDENTITY_MISMATCH"), refusalOf { register(forged) })
    }

    @Test
    fun `Отсутствие роли важнее отозванного сертификата`() {
        val agent = fixtures.enrolled(tenant)
        jdbc.update("update agent_certificates set revoked_at = now() where agent_id = ?", agent.identity.agentId)
        val forged = fixtures.forged(agent, ForgedRole.Absent)
        assertEquals(refused("CERT_ROLE_MISSING"), refusalOf { register(forged) })
    }

    @Test
    fun `Строка лога отказа по роли называет агента и тенант, но не значение роли`() {
        val agent = fixtures.enrolled(tenant)
        val forged = fixtures.forged(agent, ForgedRole.Text(MARKER))

        val logs = captureLogs { assertEquals(refused("CERT_ROLE_INVALID"), refusalOf { register(forged) }) }

        val line = logs.single { "reason=CERT_ROLE_INVALID" in it }
        assertTrue("agent=${agent.identity.agentId}" in line, line)
        assertTrue("tenant=$tenant" in line, line)
        assertFalse(logs.any { MARKER in it }, logs.toString())
    }

    @Test
    fun `Сертификат с ролью source проходит S3 у агента-источника`() {
        val source = fixtures.enrolled(tenant, AgentRole.SOURCE)
        assertEquals(source.identity.agentId.toString(), register(source).agentId)
    }

    @Test
    fun `Сертификат с ролью verifier проходит S3 у агента-верификатора`() {
        val verifier = fixtures.enrolled(tenant, AgentRole.VERIFIER)
        assertEquals(verifier.identity.agentId.toString(), register(verifier).agentId)
    }

    @Test
    fun `Отказ базы при проверке роли не пропускает агента`() {
        val verifier = fixtures.enrolled(tenant, AgentRole.VERIFIER)
        val failure = JDBCConnectionException("down", SQLException("down"))
        Mockito.doThrow(failure).`when`(sessions).system(anything<(Session) -> Any?> { null })

        val outcome = refusalOf { register(verifier) }

        assertNotEquals(Status.Code.OK, outcome.code)
        assertFalse(outcome.reason in ROLE_REASONS, "$outcome")
        Mockito.reset(sessions)
        assertEquals(
            0,
            jdbc.queryForObject(
                "select count(*) from agents where machine_id is not null and id = ?",
                Int::class.java,
                verifier.identity.agentId,
            ),
        )
    }
}
