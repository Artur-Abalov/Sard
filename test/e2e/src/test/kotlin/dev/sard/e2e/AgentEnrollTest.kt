// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import io.grpc.Status
import org.junit.jupiter.api.extension.RegisterExtension
import java.security.KeyFactory
import java.security.SecureRandom
import java.security.Signature
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.security.spec.PKCS8EncodedKeySpec
import java.util.Base64
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotEquals
import kotlin.test.assertTrue

/**
 * The `@e2e` scenarios of docs/specs/agent/agent-enroll.feature: `sard-agent enroll` in an agent
 * container against the real server. Test names quote the scenario titles; an outline's example
 * follows its title after a dash. "INTERNAL_RETRYABLE" stops the database, so it has its own
 * installation ([AgentEnrollRetryableTest]).
 *
 * The spec's background maps onto the environment: the server's CA is the one its first start
 * created (fingerprint F, in every token), tenant "A" is the open core's only tenant, the agent's
 * config names `server.address` and tls files that do not exist yet ([AgentHost]).
 */
class AgentEnrollTest {
    @Test
    fun `Регистрация по активному токену записывает ключ, сертификат и бандл`() {
        val token = EnrollmentTokens.create(sard)
        val host = AgentHost(sard)

        val exit = host.enroll(token)

        assertEquals(SUCCESS, exit.code, exit.stderr)
        val files = host.credentials(AgentEnroller.agentIdOf(exit))
        val chain = certificates(files.chainPem)
        val root = certificates(files.caPem).single()
        assertTrue(keyMatches(files.keyPem, chain.first()), "the certificate's public key is not the key's")
        assertEquals(token.substringAfterLast('.'), ServerTls.fingerprint(root))
        verifyChain(chain, root)
    }

    @Test
    fun `Агент регистрируется с именем хоста и в тенанте токена`() {
        val issued = EnrollmentTokens.issue(sard)

        val agentId = AgentEnroller.enroll(AgentHost(sard, hostname = "db1"), issued.token)

        assertEquals(EnrollmentTokens.DEFAULT_TENANT to "db1", tenantAndHostname(agentId))
        assertEquals("used", EnrollmentTokens.status(sard, issued.id))
        assertEquals(agentId, EnrollmentTokens.agentOf(sard, issued.id))
    }

    @Test
    fun `Выданные файлы принимаются сервером как сертификат агента`() {
        val agent = AgentEnroller.enroll(sard, EnrollmentTokens.create(sard))

        val credentials = agent.host.credentials(agent.agentId).channelCredentials()

        assertEquals(Status.Code.UNIMPLEMENTED, ServerTls.renewCertificateStatus(sard, credentials))
    }

    @Test
    fun `Испорченная копия настоящего токена не расходует его`() {
        val issued = EnrollmentTokens.issue(sard)
        val agents = agentCount()

        val exit = AgentHost(sard).enroll(issued.token.dropLast(1))

        assertEquals(USAGE, exit.code, exit.stderr)
        assertNotRegistered(issued.id, agents)
    }

    @Test
    fun `Отказ из-за существующей идентичности не расходует токен`() {
        val host = AgentHost(sard)
        AgentEnroller.enroll(host, EnrollmentTokens.create(sard))
        val second = EnrollmentTokens.issue(sard)
        val agents = agentCount()

        val exit = host.enroll(second.token)

        assertEquals(IDENTITY_EXISTS, exit.code, exit.stderr)
        assertNotRegistered(second.id, agents)
    }

    @Test
    fun `--force заменяет ключ, сертификат и бандл новой личностью`() {
        val host = AgentHost(sard)
        val previousId = AgentEnroller.enroll(host, EnrollmentTokens.create(sard))
        val previousKey = String(host.read(AgentHost.KEY_FILE)).also(sard::secret)

        val agentId = AgentEnroller.enroll(host, EnrollmentTokens.create(sard), "--force")

        val files = host.credentials(agentId)
        assertNotEquals(previousId, agentId)
        assertNotEquals(previousKey, files.keyPem, "the key was not replaced")
        assertTrue(keyMatches(files.keyPem, certificates(files.chainPem).first()), "the new certificate's public key is not the new key's")
    }

    @Test
    fun `Прежний агент после --force остаётся в консоли`() {
        val host = AgentHost(sard)
        val previousId = AgentEnroller.enroll(host, EnrollmentTokens.create(sard))

        AgentEnroller.enroll(host, EnrollmentTokens.create(sard), "--force")

        val answer = SardApi(sard).get("/api/v1/agents/$previousId")
        assertEquals(SardApi.HTTP_OK, answer.status)
        assertEquals(previousId, answer.field("id"))
    }

    @Test
    fun `Токен с отпечатком чужого CA не расходует настоящий токен`() {
        val issued = EnrollmentTokens.issue(sard)
        val agents = agentCount()

        val exit = AgentHost(sard).enroll(issued.token.substringBeforeLast('.') + "." + TEST_VECTOR_FINGERPRINT)

        assertEquals(TRUST, exit.code, exit.stderr)
        assertNotRegistered(issued.id, agents)
    }

    @Test
    fun `Отказ сервера на настоящем сервере — уже использован`() {
        val token = EnrollmentTokens.create(sard)
        AgentEnroller.enroll(sard, token)

        assertRefused(token, "TOKEN_USED")
    }

    @Test
    fun `Отказ сервера на настоящем сервере — правильно оформлен, но не выдавался`() {
        val fingerprint = EnrollmentTokens.create(sard).substringAfterLast('.')
        val token = EnrollmentTokens.format(ByteArray(SECRET_BYTES).also(SecureRandom()::nextBytes), fingerprint).also(sard::secret)

        assertRefused(token, "TOKEN_UNKNOWN")
    }

    @Test
    fun `Отказ сервера на настоящем сервере — истёк`() {
        val issued = EnrollmentTokens.issue(sard)
        EnrollmentTokens.expire(sard, issued.id)

        assertRefused(issued.token, "TOKEN_EXPIRED")
    }

    @Test
    fun `Отказ сервера на настоящем сервере — отозван`() {
        val issued = EnrollmentTokens.issue(sard)
        EnrollmentTokens.revoke(sard, issued.id)

        assertRefused(issued.token, "TOKEN_REVOKED")
    }

    private fun assertRefused(
        token: String,
        reason: String,
    ) {
        val exit = AgentHost(sard).enroll(token)
        assertEquals(TOKEN_REFUSED, exit.code, exit.stderr)
        assertTrue(reason in exit.stderr, "the message does not name $reason: ${exit.stderr}")
    }

    /** "Сервер не получил регистрацию" for @e2e: the token is active, the number of agents unchanged. */
    private fun assertNotRegistered(
        tokenId: UUID,
        agentsBefore: Long,
    ) {
        assertEquals("active", EnrollmentTokens.status(sard, tokenId))
        assertEquals(agentsBefore, agentCount())
    }

    private fun agentCount(): Long =
        sard.database().use { connection ->
            connection.createStatement().use { statement ->
                statement.executeQuery("SELECT count(*) FROM agents").use {
                    it.next()
                    it.getLong(1)
                }
            }
        }

    private fun tenantAndHostname(agentId: String): Pair<UUID, String>? =
        sard.database().use { connection ->
            connection.prepareStatement("SELECT tenant_id, hostname FROM agents WHERE id = ?").use { query ->
                query.setObject(1, UUID.fromString(agentId))
                query.executeQuery().use { if (it.next()) it.getObject(1, UUID::class.java) to it.getString(2) else null }
            }
        }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()

        // Exit codes of the classes in the spec's table (agent/cmd/sard-agent/enroll_flags.go).
        private const val SUCCESS = 0
        private const val USAGE = 2
        private const val TOKEN_REFUSED = 3
        private const val IDENTITY_EXISTS = 4
        private const val TRUST = 5

        private const val SECRET_BYTES = 32

        /** The fingerprint of the token test vector (docs/specs/enrollment-token.md); no real CA has it. */
        private const val TEST_VECTOR_FINGERPRINT = "8544e2352a80a3d403eed68f8bb4ff271d0ce02c09c1d0faf6f090cea423be9f"

        fun certificates(pem: String): List<X509Certificate> =
            CertificateFactory.getInstance("X.509").generateCertificates(pem.byteInputStream()).map { it as X509Certificate }

        /** Whether [keyPem] (PKCS #8, as enroll writes it) is the private half of [certificate]'s key: sign, then verify. */
        fun keyMatches(
            keyPem: String,
            certificate: X509Certificate,
        ): Boolean {
            val der = Base64.getMimeDecoder().decode(keyPem.lines().filterNot { it.startsWith("-----") }.joinToString(""))
            val key = KeyFactory.getInstance(certificate.publicKey.algorithm).generatePrivate(PKCS8EncodedKeySpec(der))
            val algorithm = if (key.algorithm == "EC") "SHA256withECDSA" else "SHA256withRSA"
            val data = "sard e2e".toByteArray()
            val signature = Signature.getInstance(algorithm).apply { initSign(key) }.run { update(data); sign() }
            return Signature.getInstance(algorithm).apply { initVerify(certificate.publicKey) }.run { update(data); verify(signature) }
        }

        /** Each certificate is signed by the next, the last by [root]; throws otherwise. */
        fun verifyChain(
            chain: List<X509Certificate>,
            root: X509Certificate,
        ) {
            (chain + root).zipWithNext().forEach { (certificate, issuer) -> certificate.verify(issuer.publicKey) }
            root.verify(root.publicKey)
        }
    }
}
