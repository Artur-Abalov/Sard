// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.persistence.Agent
import dev.sard.server.persistence.AgentCertificateRecord
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.AgentCsr
import dev.sard.server.pki.AgentIdentity
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.pki.InvalidCsrException
import dev.sard.server.role.AgentRole
import org.hibernate.LockMode
import org.hibernate.Session
import org.slf4j.LoggerFactory
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.coroutines.cancellation.CancellationException

private val log = LoggerFactory.getLogger(CertificateRenewal::class.java)

private const val SERIAL_RADIX = 16
private const val NANOS_PER_SECOND = 1_000_000_000L

private const val LATEST_ISSUE = "select max(c.issuedAt) from AgentCertificateRecord c where c.agentId = :agent"

/** A renewal replaces the certificates of the agent that no one confirmed (C1, П7). */
private const val SUPERSEDE_UNCONFIRMED =
    "update AgentCertificateRecord c set c.supersededAt = :now where c.agentId = :agent " +
        "and c.supersededAt is null and c.predecessorSerial is not null " +
        "and exists (select 1 from AgentCertificateRecord p " +
        "where p.serial = c.predecessorSerial and p.supersededAt is null)"

private const val PREDECESSOR_OF = "select c.predecessorSerial from AgentCertificateRecord c where c.serial = :serial"
private const val SUPERSEDE_ONE =
    "update AgentCertificateRecord set supersededAt = :now where serial = :serial and supersededAt is null"

/** The new certificate of a renewal: what the agent receives and what the log may say about it. */
class RenewedCertificate(
    val chainPem: String,
    val serial: String,
    val notAfter: Instant,
)

/**
 * The renewal cannot go through (C1, П4). [retryAfter] is set for [Reason.RENEWAL_TOO_FREQUENT] only: the time
 * until the agent may ask again, in whole seconds, at least one. The message never names a cause.
 */
class RenewalRejectedException(
    val reason: Reason,
    val retryAfter: Duration? = null,
    cause: Throwable? = null,
) : RuntimeException("renewal rejected: ${reason.name}", cause) {
    /** Order matches the order of the checks: the first that fails decides the answer. */
    enum class Reason {
        CSR_INVALID,
        CSR_KEY_REUSED,
        RENEWAL_TOO_FREQUENT,
        INTERNAL_RETRYABLE,
    }
}

private typealias RenewalReason = RenewalRejectedException.Reason

/** Who asks for a renewal: the agent of the call's certificate, and the key that certificate holds (SPKI, DER). */
class RenewalCaller(
    val tenantId: UUID,
    val agentId: UUID,
    val serial: String,
    val key: ByteArray,
)

/**
 * RenewCertificate and ConfirmCertificate (C1). A renewal issues a new certificate for the same agent and role on
 * a new key; once the new certificate confirms itself, the one it replaced stops authenticating new calls.
 * One transaction per call: on any failure nothing changes and the rate limit is not spent.
 */
class CertificateRenewal(
    private val sessions: TenantSessions,
    private val ca: CertificateAuthority,
    private val clock: Clock,
    /** At most one issue per agent in this time (C1, П12). */
    private val interval: Duration,
) {
    fun renew(
        caller: RenewalCaller,
        csrDer: ByteArray,
    ): RenewedCertificate =
        try {
            val csrKey = csrKey(csrDer)
            if (csrKey.contentEquals(caller.key)) throw RenewalRejectedException(RenewalReason.CSR_KEY_REUSED)
            val renewed = wrapUnexpected { sessions.inTenant(caller.tenantId) { renewIn(it, caller, csrDer) } }
            log.info(
                "certificate renewed: agent={} tenant={} old={} new={} notAfter={}",
                caller.agentId,
                caller.tenantId,
                caller.serial,
                renewed.serial,
                renewed.notAfter,
            )
            renewed
        } catch (e: RenewalRejectedException) {
            logRefusal(e, caller)
            throw e
        }

    /** Marks the certificate this one replaced as superseded; idempotent (C1, П9). */
    fun confirm(caller: RenewalCaller) {
        try {
            val superseded = wrapUnexpected { sessions.inTenant(caller.tenantId) { confirmIn(it, caller) } }
            if (superseded != null) {
                val line = "certificate confirmed: agent={} serial={} superseded={}"
                log.info(line, caller.agentId, caller.serial, superseded)
            }
        } catch (e: RenewalRejectedException) {
            logRefusal(e, caller)
            throw e
        }
    }

    private fun logRefusal(
        e: RenewalRejectedException,
        caller: RenewalCaller,
    ) {
        if (e.reason == RenewalReason.INTERNAL_RETRYABLE) {
            log.error("certificate renewal of agent {} failed (serial {})", caller.agentId, caller.serial, e)
        } else {
            val line = "certificate renewal refused: reason={} agent={} serial={}"
            log.warn(line, e.reason, caller.agentId, caller.serial)
        }
    }

    private fun csrKey(csrDer: ByteArray): ByteArray =
        try {
            AgentCsr.verifiedKey(csrDer).encoded
        } catch (e: InvalidCsrException) {
            throw RenewalRejectedException(RenewalReason.CSR_INVALID, cause = e)
        }

    private fun renewIn(
        session: Session,
        caller: RenewalCaller,
        csrDer: ByteArray,
    ): RenewedCertificate {
        val now = clock.instant()
        // The lock on the agent row serialises renewals of one agent, so the rate check and the insert are one step.
        val agent = checkNotNull(session.find(Agent::class.java, caller.agentId, LockMode.PESSIMISTIC_WRITE))
        requireIntervalPassed(session, caller.agentId, now)
        val identity = AgentIdentity(caller.tenantId, caller.agentId)
        val issued = ca.issueAgentCertificate(csrDer, identity, AgentRole.of(agent.role))
        session
            .createMutationQuery(SUPERSEDE_UNCONFIRMED)
            .setParameter("now", now)
            .setParameter("agent", caller.agentId)
            .executeUpdate()
        val serial = issued.serial.toString(SERIAL_RADIX)
        session.persist(
            AgentCertificateRecord(serial, caller.agentId, now, issued.notAfter, predecessorSerial = caller.serial),
        )
        return RenewedCertificate(issued.chainPem, serial, issued.notAfter)
    }

    private fun requireIntervalPassed(
        session: Session,
        agentId: UUID,
        now: Instant,
    ) {
        val latest =
            session
                .createSelectionQuery(LATEST_ISSUE, Instant::class.java)
                .setParameter("agent", agentId)
                .uniqueResult() ?: return
        val wait = Duration.between(now, latest + interval)
        if (wait.isPositive) {
            throw RenewalRejectedException(RenewalReason.RENEWAL_TOO_FREQUENT, retryAfter = wholeSeconds(wait))
        }
    }

    /** Rounded up to whole seconds; a positive [wait] is therefore at least one second. */
    private fun wholeSeconds(wait: Duration): Duration {
        val seconds = (wait.toNanos() + NANOS_PER_SECOND - 1) / NANOS_PER_SECOND
        return Duration.ofSeconds(seconds)
    }

    /** The serial this call superseded, or null when it changed nothing. */
    private fun confirmIn(
        session: Session,
        caller: RenewalCaller,
    ): String? {
        val predecessor =
            session
                .createSelectionQuery(PREDECESSOR_OF, String::class.java)
                .setParameter("serial", caller.serial)
                .uniqueResult() ?: return null
        val changed =
            session
                .createMutationQuery(SUPERSEDE_ONE)
                .setParameter("now", clock.instant())
                .setParameter("serial", predecessor)
                .executeUpdate()
        return predecessor.takeIf { changed == 1 }
    }

    /** Whatever a collaborator throws, other than a refusal of ours, is the retryable failure (C1, П4). */
    @Suppress("TooGenericExceptionCaught")
    private fun <T> wrapUnexpected(block: () -> T): T =
        try {
            block()
        } catch (e: RenewalRejectedException) {
            throw e
        } catch (e: CancellationException) {
            throw e
        } catch (e: InvalidCsrException) {
            throw RenewalRejectedException(RenewalReason.CSR_INVALID, cause = e)
        } catch (e: Exception) {
            throw RenewalRejectedException(RenewalReason.INTERNAL_RETRYABLE, cause = e)
        }
}
