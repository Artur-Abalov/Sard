// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.Pem
import io.grpc.ChannelCredentials
import io.grpc.Grpc
import io.grpc.ManagedChannel
import io.grpc.TlsChannelCredentials
import org.bouncycastle.asn1.x500.X500Name
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder
import org.bouncycastle.pkcs.jcajce.JcaPKCS10CertificationRequestBuilder
import org.springframework.jdbc.core.ConnectionCallback
import org.springframework.jdbc.core.JdbcTemplate
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

    fun csr(keys: KeyPair): ByteArray {
        val signer = JcaContentSignerBuilder("SHA256withECDSA").build(keys.private)
        return JcaPKCS10CertificationRequestBuilder(X500Name("CN=test"), keys.public).build(signer).encoded
    }

    fun enrolled(tenant: UUID): EnrolledAgent {
        val keys = newKeys()
        val agent = enrollment.enroll(tokens.create(tenant, TTL).reveal(), csr(keys), "host-$tenant")
        return EnrolledAgent(AgentIdentity(tenant, agent.agentId), agent.certificateChainPem, keys)
    }

    fun presenting(agent: EnrolledAgent): ChannelCredentials =
        TlsChannelCredentials
            .newBuilder()
            .trustManager(ca.caBundlePem().byteInputStream())
            .keyManager(agent.chainPem.byteInputStream(), Pem.privateKey(agent.keys.private).byteInputStream())
            .build()

    fun channel(agent: EnrolledAgent): ManagedChannel =
        Grpc.newChannelBuilderForAddress("localhost", grpcPort, presenting(agent)).build().also { channels += it }

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

        /** Children first: every table an agent's enrollment and registration writes. */
        val TENANT_TABLES =
            listOf("agent_plugins", "agent_repositories", "agent_certificates", "enrollment_tokens", "agents")

        val DELETE_TENANT =
            listOf("select id from agents where tenant_id = ? for update") +
                TENANT_TABLES.map { "delete from $it where tenant_id = ?" } +
                "delete from tenants where id = ?"
    }
}
