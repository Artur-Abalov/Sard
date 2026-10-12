// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import dev.sard.server.agents.AgentAuthResult.Accepted
import dev.sard.server.agents.AgentAuthResult.Rejected
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.AgentRole
import dev.sard.server.pki.CertificateRole
import dev.sard.server.pki.FakeCaLedger
import dev.sard.server.pki.FileCertificateAuthority
import dev.sard.server.pki.PkiFixtures.AGENT
import dev.sard.server.pki.PkiFixtures.CLOCK
import dev.sard.server.pki.PkiFixtures.NOW
import dev.sard.server.pki.PkiFixtures.SERVER_NAMES
import dev.sard.server.pki.PkiFixtures.certificates
import dev.sard.server.pki.PkiFixtures.random
import dev.sard.server.pki.PkiFixtures.resource
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import java.time.Duration
import java.util.UUID
import kotlin.test.Test
import kotlin.test.assertEquals

private const val SERIAL = "8f0e5c2a9b7d4e6f8a1b2c3d4e5f6a7b"
private val SOURCE = CertificateRole.Valid(AgentRole.SOURCE)
private val VERIFIER = CertificateRole.Valid(AgentRole.VERIFIER)
private val STANDING =
    CertificateStanding(
        AGENT,
        notAfter = NOW + Duration.ofDays(1),
        revokedAt = null,
        agentRevokedAt = null,
        role = AgentRole.SOURCE,
    )
private val PRESENTED = PresentedCertificate(SERIAL, AGENT, SOURCE)
private val PRINCIPAL = AgentPrincipal(AGENT.agentId, AGENT.tenantId, SERIAL, AgentRole.SOURCE)

@MutFlowTest
class AgentAuthenticatorTest {
    @TempDir
    lateinit var tmp: Path

    private fun authenticate(
        presented: PresentedCertificate?,
        standing: CertificateStanding? = STANDING,
    ): AgentAuthResult {
        val standings = CertificateStandings { serial -> standing.takeIf { serial == SERIAL } }
        return MutFlow.underTest { AgentAuthenticator(standings, CLOCK).authenticate(presented) }
    }

    @Test
    fun `a valid certificate of a live agent yields its principal`() {
        assertEquals(Accepted(PRINCIPAL), authenticate(PRESENTED))
    }

    @Test
    fun `no certificate is CERT_MISSING`() {
        assertEquals(Rejected(AgentAuthFailure.CERT_MISSING, serial = null, agentId = null), authenticate(null))
    }

    @Test
    fun `a serial with no record is CERT_UNKNOWN and logs the agent and tenant the certificate claims`() {
        val stranger = PresentedCertificate("1".repeat(32), AGENT, SOURCE)
        val expected = Rejected(AgentAuthFailure.CERT_UNKNOWN, stranger.serial, AGENT.agentId, AGENT.tenantId)
        assertEquals(expected, authenticate(stranger))
        val anonymous = PresentedCertificate("1".repeat(32), identity = null, role = CertificateRole.Missing)
        assertEquals(Rejected(AgentAuthFailure.CERT_UNKNOWN, anonymous.serial, null), authenticate(anonymous))
    }

    @Test
    fun `a certificate naming another agent, another tenant or nobody is CERT_IDENTITY_MISMATCH`() {
        val claims =
            listOf(
                AGENT.copy(agentId = UUID.randomUUID()),
                AGENT.copy(tenantId = UUID.randomUUID()),
                null,
            )
        for (claim in claims) {
            val expected = Rejected(AgentAuthFailure.CERT_IDENTITY_MISMATCH, SERIAL, AGENT.agentId)
            assertEquals(expected, authenticate(PresentedCertificate(SERIAL, claim, SOURCE)), "$claim")
        }
    }

    @Test
    fun `a revoked certificate is CERT_REVOKED`() {
        val revoked = STANDING.copy(revokedAt = NOW + Duration.ofDays(1))
        assertEquals(Rejected(AgentAuthFailure.CERT_REVOKED, SERIAL, AGENT.agentId), authenticate(PRESENTED, revoked))
    }

    @Test
    fun `a certificate is CERT_EXPIRED from its recorded not_after on`() {
        val expired = Rejected(AgentAuthFailure.CERT_EXPIRED, SERIAL, AGENT.agentId)
        assertEquals(expired, authenticate(PRESENTED, STANDING.copy(notAfter = NOW)))
        assertEquals(expired, authenticate(PRESENTED, STANDING.copy(notAfter = NOW - Duration.ofSeconds(1))))
        val live = authenticate(PRESENTED, STANDING.copy(notAfter = NOW + Duration.ofMillis(1)))
        assertEquals(Accepted(PRINCIPAL), live)
    }

    @Test
    fun `a revoked agent is AGENT_REVOKED`() {
        val revoked = STANDING.copy(agentRevokedAt = NOW + Duration.ofDays(1))
        assertEquals(Rejected(AgentAuthFailure.AGENT_REVOKED, SERIAL, AGENT.agentId), authenticate(PRESENTED, revoked))
    }

    @Test
    fun `a mismatch outranks revocation, and a revoked certificate outranks its expiry and its agent`() {
        val everything = STANDING.copy(notAfter = NOW, revokedAt = NOW, agentRevokedAt = NOW)
        val stranger = PresentedCertificate(SERIAL, AgentIdentity(UUID.randomUUID(), UUID.randomUUID()), SOURCE)
        assertEquals(AgentAuthFailure.CERT_IDENTITY_MISMATCH, (authenticate(stranger, everything) as Rejected).failure)
        assertEquals(AgentAuthFailure.CERT_REVOKED, (authenticate(PRESENTED, everything) as Rejected).failure)
        val expiredOfRevokedAgent = STANDING.copy(notAfter = NOW, agentRevokedAt = NOW)
        val expired = authenticate(PRESENTED, expiredOfRevokedAgent) as Rejected
        assertEquals(AgentAuthFailure.CERT_EXPIRED, expired.failure)
    }

    @Test
    fun `a presented certificate is its serial in lower-case hex and its URI identity`() {
        val ca = FileCertificateAuthority(tmp.resolve("pki"), SERVER_NAMES, CLOCK, random(), FakeCaLedger())
        val issued = ca.issueAgentCertificate(resource("agent-p256.csr"), AGENT, AgentRole.VERIFIER)
        val presented = MutFlow.underTest { PresentedCertificate.of(certificates(issued.chainPem).first()) }
        assertEquals(PresentedCertificate(issued.serial.toString(16), AGENT, VERIFIER), presented)
    }

    private fun roleRefusal(
        failure: AgentAuthFailure,
        standing: CertificateStanding = STANDING,
    ) = Rejected(failure, SERIAL, standing.identity.agentId, standing.identity.tenantId)

    @Test
    fun `a certificate without a role is CERT_ROLE_MISSING and the log may name its agent and tenant`() {
        val bare = PresentedCertificate(SERIAL, AGENT, CertificateRole.Missing)
        assertEquals(roleRefusal(AgentAuthFailure.CERT_ROLE_MISSING), authenticate(bare))
    }

    @Test
    fun `a certificate with a role that is neither source nor verifier is CERT_ROLE_INVALID`() {
        val odd = PresentedCertificate(SERIAL, AGENT, CertificateRole.Invalid)
        assertEquals(roleRefusal(AgentAuthFailure.CERT_ROLE_INVALID), authenticate(odd))
    }

    @Test
    fun `a certificate role other than the agent's is CERT_IDENTITY_MISMATCH both ways`() {
        val verifierCertificate = PresentedCertificate(SERIAL, AGENT, VERIFIER)
        val mismatch = Rejected(AgentAuthFailure.CERT_IDENTITY_MISMATCH, SERIAL, AGENT.agentId)
        assertEquals(mismatch, authenticate(verifierCertificate))
        val verifier = STANDING.copy(role = AgentRole.VERIFIER)
        assertEquals(mismatch, authenticate(PRESENTED, verifier))
    }

    @Test
    fun `a verifier whose certificate says verifier is accepted with its role`() {
        val verifier = STANDING.copy(role = AgentRole.VERIFIER)
        val expected = Accepted(PRINCIPAL.copy(role = AgentRole.VERIFIER))
        assertEquals(expected, authenticate(PresentedCertificate(SERIAL, AGENT, VERIFIER), verifier))
    }

    @Test
    fun `the checks run in the documented order, the certificate before the state of the record`() {
        val everythingWrong = STANDING.copy(notAfter = NOW, revokedAt = NOW, agentRevokedAt = NOW)
        val stranger = AgentIdentity(UUID.randomUUID(), UUID.randomUUID())
        val sanFirst = authenticate(PresentedCertificate(SERIAL, stranger, CertificateRole.Missing), everythingWrong)
        assertEquals(AgentAuthFailure.CERT_IDENTITY_MISMATCH, (sanFirst as Rejected).failure)
        val missing = authenticate(PresentedCertificate(SERIAL, AGENT, CertificateRole.Missing), everythingWrong)
        assertEquals(AgentAuthFailure.CERT_ROLE_MISSING, (missing as Rejected).failure)
        val invalid = authenticate(PresentedCertificate(SERIAL, AGENT, CertificateRole.Invalid), everythingWrong)
        assertEquals(AgentAuthFailure.CERT_ROLE_INVALID, (invalid as Rejected).failure)
        val other = authenticate(PresentedCertificate(SERIAL, AGENT, VERIFIER), everythingWrong)
        assertEquals(AgentAuthFailure.CERT_IDENTITY_MISMATCH, (other as Rejected).failure)
        val revoked = authenticate(PRESENTED, everythingWrong)
        assertEquals(AgentAuthFailure.CERT_REVOKED, (revoked as Rejected).failure)
    }
}
