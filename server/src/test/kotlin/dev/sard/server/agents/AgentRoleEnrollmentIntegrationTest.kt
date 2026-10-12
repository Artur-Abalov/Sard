// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.protobuf.ByteString
import com.google.protobuf.UnknownFieldSet
import dev.sard.proto.agent.v1.EnrollRequest
import dev.sard.proto.agent.v1.EnrollResponse
import dev.sard.proto.agent.v1.EnrollmentServiceGrpc
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokenState
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.persistence.UuidV7
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.AgentRole
import dev.sard.server.pki.AgentRoleExtension
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.CertificateRole
import dev.sard.server.pki.IssuedCertificate
import dev.sard.server.pki.PkiFixtures
import org.bouncycastle.asn1.DEROctetString
import org.bouncycastle.asn1.DERUTF8String
import org.bouncycastle.asn1.x509.Extension
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.math.BigInteger
import java.security.SecureRandom
import java.security.cert.X509Certificate
import java.time.Clock
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNotEquals

/** A CA that signs properly but reports a serial the database refuses: the certificate record cannot be written. */
private class UnrecordableCa(
    private val delegate: CertificateAuthority,
) : CertificateAuthority by delegate {
    override fun issueAgentCertificate(
        csrDer: ByteArray,
        agent: AgentIdentity,
        role: AgentRole,
    ): IssuedCertificate {
        val issued = delegate.issueAgentCertificate(csrDer, agent, role)
        return IssuedCertificate(issued.chainPem, BigInteger.ONE, issued.notAfter)
    }
}

/** V1, rules "Enroll пишет роль ... одной транзакцией" and "Роль никогда не берётся из сообщений агента". */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class AgentRoleEnrollmentIntegrationTest(
    @Autowired private val ca: CertificateAuthority,
    @Autowired private val enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val clock: Clock,
    @Autowired private val jdbc: JdbcTemplate,
    @LocalGrpcServerPort grpcPort: Int,
) {
    private val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)
    private val tenant = fixtures.tenant()

    @AfterTest
    fun cleanUp() = fixtures.close()

    private fun enroll(
        token: String,
        keys: java.security.KeyPair = fixtures.newKeys(),
        csr: ByteArray = fixtures.csr(keys),
        hostname: String = "db1",
        unknownField: Boolean = false,
    ): EnrollResponse {
        val builder =
            EnrollRequest
                .newBuilder()
                .setEnrollmentToken(token)
                .setCsrDer(ByteString.copyFrom(csr))
                .setHostname(hostname)
        if (unknownField) {
            val verifier = UnknownFieldSet.Field.newBuilder().addLengthDelimited(ByteString.copyFromUtf8("verifier"))
            builder.setUnknownFields(UnknownFieldSet.newBuilder().addField(UNKNOWN_FIELD, verifier.build()).build())
        }
        return EnrollmentServiceGrpc.newBlockingStub(fixtures.anonymousChannel()).enroll(builder.build())
    }

    private fun certificateOf(response: EnrollResponse): X509Certificate {
        val chain = PkiFixtures.certificates(response.certificateChainPem)
        return chain.first()
    }

    private fun roleOfCertificate(response: EnrollResponse) = AgentRoleExtension.read(certificateOf(response))

    private fun roleInDatabase(response: EnrollResponse) =
        jdbc.queryForObject("select role from agents where id = ?::uuid", String::class.java, response.agentId)

    private fun extensionCount(certificate: X509Certificate) =
        org.bouncycastle.cert.jcajce
            .JcaX509CertificateHolder(certificate)
            .extensions.extensionOIDs
            .count { it.id == AgentRoleExtension.OID }

    @Test
    fun `Регистрация по токену verifier выдаёт сертификат с ролью verifier`() {
        val response = enroll(tokens.create(tenant, role = AgentRole.VERIFIER).reveal())

        val certificate = certificateOf(response)
        assertEquals(CertificateRole.Valid(AgentRole.VERIFIER), AgentRoleExtension.read(certificate))
        assertEquals(true, AgentRoleExtension.OID in certificate.nonCriticalExtensionOIDs)
        assertEquals("verifier", roleInDatabase(response))
    }

    @Test
    fun `Регистрация по токену без указанной роли выдаёт сертификат с ролью source`() {
        val response = enroll(tokens.create(tenant).reveal())

        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), roleOfCertificate(response))
        assertEquals("source", roleInDatabase(response))
    }

    @Test
    fun `Встроенный агент sard-self регистрируется с ролью source`() {
        val response = enroll(tokens.replaceBuiltin(tenant).reveal(), hostname = "sard-self")

        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), roleOfCertificate(response))
        assertEquals(
            listOf("source" to true),
            jdbc.queryForList("select role, builtin from agents where id = ?::uuid", response.agentId).map {
                it["role"] to it["builtin"]
            },
        )
    }

    @Test
    fun `Сбой записи агента не оставляет ни сертификата, ни агента с ролью`() {
        val issued = tokens.create(tenant, role = AgentRole.VERIFIER)
        val broken = Enrollment(sessions, tokens, UnrecordableCa(ca), clock, UuidV7(clock, SecureRandom()))

        val error =
            assertFailsWith<dev.sard.server.enrollment.EnrollmentRejectedException> {
                broken.enroll(issued.reveal(), fixtures.csr(fixtures.newKeys()), "db1")
            }

        assertEquals(dev.sard.server.enrollment.EnrollmentRejectedException.Reason.INTERNAL_RETRYABLE, error.reason)
        assertEquals(0, jdbc.queryForObject("select count(*) from agents where tenant_id = ?", Int::class.java, tenant))
        assertEquals(EnrollmentTokenState.ACTIVE, tokens.get(tenant, issued.id)?.state)
        assertEquals(AgentRole.VERIFIER, tokens.get(tenant, issued.id)?.role)
    }

    @Test
    fun `Расширение роли в CSR не попадает в сертификат`() {
        val keys = fixtures.newKeys()
        val asked = Extension(AgentRoleExtension.IDENTIFIER, false, DEROctetString(DERUTF8String("verifier")))
        val csr = fixtures.csr(keys, extensions = listOf(asked))

        val response = enroll(tokens.create(tenant, role = AgentRole.SOURCE).reveal(), keys, csr)

        val certificate = certificateOf(response)
        assertEquals(1, extensionCount(certificate))
        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), AgentRoleExtension.read(certificate))
        assertEquals("source", roleInDatabase(response))
    }

    @Test
    fun `Критичное расширение роли в CSR игнорируется`() {
        val keys = fixtures.newKeys()
        val asked = Extension(AgentRoleExtension.IDENTIFIER, true, DEROctetString(DERUTF8String("source")))
        val csr = fixtures.csr(keys, extensions = listOf(asked))

        val response = enroll(tokens.create(tenant, role = AgentRole.VERIFIER).reveal(), keys, csr)

        val certificate = certificateOf(response)
        assertEquals(1, extensionCount(certificate))
        assertEquals(CertificateRole.Valid(AgentRole.VERIFIER), AgentRoleExtension.read(certificate))
        assertEquals(emptyList(), certificate.criticalExtensionOIDs.orEmpty().filter { it == AgentRoleExtension.OID })
    }

    @Test
    fun `Слово verifier в subject CSR не меняет роль`() {
        val keys = fixtures.newKeys()
        val csr = fixtures.csr(keys, subject = "CN=verifier, OU=verifier")

        val response = enroll(tokens.create(tenant, role = AgentRole.SOURCE).reveal(), keys, csr)

        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), roleOfCertificate(response))
    }

    @Test
    fun `Неизвестное поле в запросе Enroll не меняет роль`() {
        val response = enroll(tokens.create(tenant, role = AgentRole.SOURCE).reveal(), unknownField = true)

        assertEquals("source", roleInDatabase(response))
        assertEquals(CertificateRole.Valid(AgentRole.SOURCE), roleOfCertificate(response))
    }

    @Test
    fun `Новая регистрация хоста с другой ролью создаёт нового агента и не меняет прежнего`() {
        val first = enroll(tokens.create(tenant, role = AgentRole.SOURCE).reveal())
        val second = enroll(tokens.create(tenant, role = AgentRole.VERIFIER).reveal())

        assertNotEquals(first.agentId, second.agentId)
        assertEquals("verifier", roleInDatabase(second))
        assertEquals("source", roleInDatabase(first))
    }

    @Test
    fun `Регистрация без сертификата роль не проверяет`() {
        val response = enroll(tokens.create(tenant).reveal())

        val sql = "select tenant_id from agents where id = ?::uuid"
        assertEquals(tenant, jdbc.queryForObject(sql, UUID::class.java, response.agentId))
    }

    private companion object {
        const val UNKNOWN_FIELD = 99
    }
}
