// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.proto.agent.v1.RegisterRequest
import dev.sard.server.api.MACHINE_ID
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.ForgedCertificates
import dev.sard.server.pki.ForgedRole
import dev.sard.server.pki.Pem
import dev.sard.server.role.AgentRole
import io.grpc.ChannelCredentials
import io.grpc.Grpc
import io.grpc.ManagedChannel
import io.grpc.TlsChannelCredentials
import org.bouncycastle.asn1.pkcs.PKCSObjectIdentifiers
import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.asn1.x509.Extension
import org.bouncycastle.asn1.x509.Extensions
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import org.bouncycastle.pkcs.jcajce.JcaPKCS10CertificationRequestBuilder
import org.springframework.jdbc.core.ConnectionCallback
import org.springframework.jdbc.core.JdbcTemplate
import java.math.BigInteger
import java.security.KeyPair
import java.security.KeyPairGenerator
import java.security.spec.ECGenParameterSpec
import java.time.Duration
import java.util.UUID

/** A key pair and the certificate chain the server issued for it through Enroll. */
class EnrolledAgent(
    val identity: AgentIdentity,
    val chainPem: String,
    val keys: KeyPair,
)

/**
 * Agents enrolled the way a real one is (token, CSR, Enroll) and mTLS channels presenting
 * their certificates, for AgentService integration tests. Owns the channels and tenants it
 * creates; [close] releases both.
 */
class AgentGrpcFixtures(
    private val ca: CertificateAuthority,
    private val enrollment: Enrollment,
    private val tokens: EnrollmentTokens,
    private val jdbc: JdbcTemplate,
    private val grpcPort: Int,
) : AutoCloseable {
    private val channels = mutableListOf<ManagedChannel>()
    private val tenants = mutableListOf<UUID>()

    fun tenant(): UUID {
        val id = UUID.randomUUID()
        jdbc.update("insert into tenants (id, name) values (?, ?)", id, "t-$id")
        tenants += id
        return id
    }

    fun newKeys(): KeyPair {
        val generator = KeyPairGenerator.getInstance("EC")
        generator.initialize(ECGenParameterSpec("secp256r1"))
        return generator.generateKeyPair()
    }

    /** A CSR for [keys]; [extensions] are asked for in its PKCS#9 extensionRequest attribute. */
    fun csr(
        keys: KeyPair,
        subject: String = "CN=test",
        extensions: List<Extension> = emptyList(),
    ): ByteArray {
        val signer = JcaContentSignerBuilder("SHA256withECDSA").build(keys.private)
        val builder = JcaPKCS10CertificationRequestBuilder(X500Name(subject), keys.public)
        if (extensions.isNotEmpty()) {
            val asked = Extensions(extensions.toTypedArray())
            builder.addAttribute(PKCSObjectIdentifiers.pkcs_9_at_extensionRequest, asked)
        }
        return builder.build(signer).encoded
    }

    fun enrolled(
        tenant: UUID,
        role: AgentRole = AgentRole.SOURCE,
    ): EnrolledAgent {
        val keys = newKeys()
        val token = tokens.create(tenant, TTL, role = role).reveal()
        val agent = enrollment.enroll(token, csr(keys), "host-$tenant")
        return EnrolledAgent(AgentIdentity(tenant, agent.agentId), agent.certificateChainPem, keys)
    }

    /** The serial of the certificate Enroll recorded for [agent]. */
    fun serialOf(agent: EnrolledAgent): String =
        checkNotNull(
            jdbc.queryForObject(
                "select serial from agent_certificates where agent_id = ?",
                String::class.java,
                agent.identity.agentId,
            ),
        )

    /**
     * [agent] with a certificate the CA signed for the same key, serial and SAN but with [role] in the
     * extension (or none): "поддельный сертификат" of the specification.
     */
    fun forged(
        agent: EnrolledAgent,
        role: ForgedRole,
        identity: AgentIdentity = agent.identity,
    ): EnrolledAgent {
        val serial = BigInteger(serialOf(agent), HEX)
        val certificate = ForgedCertificates.of(ca).forge(agent.keys, serial, identity, role)
        return EnrolledAgent(agent.identity, Pem.certificate(certificate), agent.keys)
    }

    fun presenting(agent: EnrolledAgent): ChannelCredentials =
        TlsChannelCredentials
            .newBuilder()
            .trustManager(ca.caBundlePem().byteInputStream())
            .keyManager(agent.chainPem.byteInputStream(), Pem.privateKey(agent.keys.private).byteInputStream())
            .build()

    /** A channel that trusts the server's CA and presents no certificate. */
    fun anonymousChannel(): ManagedChannel =
        Grpc
            .newChannelBuilderForAddress(
                "localhost",
                grpcPort,
                TlsChannelCredentials.newBuilder().trustManager(ca.caBundlePem().byteInputStream()).build(),
            ).build()
            .also { channels += it }

    fun channel(agent: EnrolledAgent): ManagedChannel =
        Grpc.newChannelBuilderForAddress("localhost", grpcPort, presenting(agent)).build().also { channels += it }

    /** A Register request that passes the rules; [machineId] is 64 lower-case hex characters. */
    fun registerRequest(
        hostname: String = "db1",
        machineId: String = MACHINE_ID,
    ): RegisterRequest.Builder =
        RegisterRequest
            .newBuilder()
            .setProtocolVersion(1)
            .setHostname(hostname)
            .setAgentVersion("0.1.0")
            .setOs("linux")
            .setArch("amd64")
            .setMachineId(machineId)

    override fun close() {
        channels.forEach { it.shutdownNow() }
        tenants.forEach(::deleteTenant)
    }

    /**
     * One transaction that first locks the tenant's agent rows, as Register does: a Register
     * still in flight on the server (its client already gone) either commits before the lock,
     * and its rows are deleted here, or waits for it and then finds no agent.
     */
    private fun deleteTenant(tenant: UUID) {
        jdbc.execute(
            ConnectionCallback { connection ->
                connection.autoCommit = false
                try {
                    for (sql in DELETE_TENANT) {
                        connection.prepareStatement(sql).use {
                            it.setObject(1, tenant)
                            it.execute()
                        }
                    }
                    connection.commit()
                } finally {
                    connection.rollback()
                    connection.autoCommit = true
                }
            },
        )
    }

    private companion object {
        val TTL: Duration = Duration.ofHours(1)
        const val HEX = 16

        /** Children first: every table an agent's enrollment and registration writes. */
        val TENANT_TABLES =
            listOf("agent_plugins", "agent_repositories", "agent_certificates", "enrollment_tokens", "agents")

        val DELETE_TENANT =
            listOf("select id from agents where tenant_id = ? for update") +
                TENANT_TABLES.map { "delete from $it where tenant_id = ?" } +
                "delete from tenants where id = ?"
    }
}
