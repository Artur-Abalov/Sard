// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.protobuf.ByteString
import com.google.rpc.ErrorInfo
import com.google.rpc.RetryInfo
import dev.sard.proto.agent.v1.AgentServiceGrpc
import dev.sard.proto.agent.v1.ConfirmCertificateRequest
import dev.sard.proto.agent.v1.ConnectResponse
import dev.sard.proto.agent.v1.RenewCertificateRequest
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures
import dev.sard.server.role.AgentRole
import io.grpc.Metadata
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto
import io.grpc.stub.StreamObserver
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.jdbc.core.JdbcTemplate
import java.security.KeyPair
import java.time.Duration
import java.time.Instant
import java.time.temporal.ChronoUnit
import java.util.UUID
import java.util.concurrent.CompletableFuture
import java.util.concurrent.TimeUnit
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/**
 * T0 of the C1 specification. The JVM checks certificate validity with the real clock during the TLS handshake,
 * so T0 lies a day in the past: a certificate issued up to a day after T0 can be presented at once.
 */
val RENEWAL_T0: Instant = Instant.now().minus(1, ChronoUnit.DAYS).truncatedTo(ChronoUnit.SECONDS)

private const val DEADLINE_SECONDS = 10L

/** A clock that starts at the real time: certificates with a short validity are presented while it still lasts. */
@TestConfiguration(proxyBeanMethods = false)
class NowClockConfiguration {
    @Bean
    fun clock() = MovableClock(Instant.now().truncatedTo(ChronoUnit.SECONDS))
}

/** The server's clock, which the test moves; starts at [RENEWAL_T0]. */
@TestConfiguration(proxyBeanMethods = false)
class RenewalClockConfiguration {
    @Bean
    fun clock() = MovableClock(RENEWAL_T0)
}

/** What a refused call says: the status code, the ErrorInfo reasons, the retry delay if the status has one. */
data class CallRefusal(
    val code: Status.Code,
    val reasons: List<String>,
    val domains: List<String> = emptyList(),
    val retryDelaySeconds: Long? = null,
) {
    val reason: String? get() = reasons.singleOrNull()
}

fun callRefusalOf(error: StatusRuntimeException): CallRefusal {
    val details = StatusProto.fromStatusAndTrailers(error.status, error.trailers ?: Metadata()).detailsList
    val infos = details.filter { it.`is`(ErrorInfo::class.java) }.map { it.unpack(ErrorInfo::class.java) }
    val retry = details.firstOrNull { it.`is`(RetryInfo::class.java) }?.unpack(RetryInfo::class.java)
    return CallRefusal(error.status.code, infos.map { it.reason }, infos.map { it.domain }, retry?.retryDelay?.seconds)
}

/** One serial of a certificate chain: lower-case hex, as the database stores it. */
fun serialOfChain(chainPem: String): String = PkiFixtures.certificate(chainPem).serialNumber.toString(16)

/** The calls of an agent that renews its certificate, over the fixtures' real mTLS channels. */
class RenewalHarness(
    val fixtures: AgentGrpcFixtures,
    val jdbc: JdbcTemplate,
    val clock: MovableClock,
) {
    fun serial(agent: EnrolledAgent): String = serialOfChain(agent.chainPem)

    /** An agent of [tenant] enrolled when the clock shows [at]; the clock stays there. */
    fun enrolledAt(
        tenant: UUID,
        at: Instant,
        role: AgentRole = AgentRole.SOURCE,
    ): EnrolledAgent {
        clock.now = at
        return fixtures.enrolled(tenant, role)
    }

    private fun stub(agent: EnrolledAgent) =
        AgentServiceGrpc.newBlockingStub(fixtures.channel(agent)).withDeadlineAfter(DEADLINE_SECONDS, TimeUnit.SECONDS)

    /** [agent] renews on a new key; the result holds the certificate of the response and the new key. */
    fun renew(
        agent: EnrolledAgent,
        keys: KeyPair = fixtures.newKeys(),
        csr: ByteArray = fixtures.csr(keys),
    ): EnrolledAgent {
        val request = RenewCertificateRequest.newBuilder().setCsrDer(ByteString.copyFrom(csr)).build()
        return EnrolledAgent(agent.identity, stub(agent).renewCertificate(request).certificateChainPem, keys)
    }

    fun confirm(agent: EnrolledAgent) {
        stub(agent).confirmCertificate(ConfirmCertificateRequest.getDefaultInstance())
    }

    /** The status a Connect stream opened by [agent] ends with at once (a refused call); it is never accepted here. */
    fun connectRefusal(agent: EnrolledAgent): CallRefusal {
        val ended = CompletableFuture<Throwable>()
        val observer =
            object : StreamObserver<ConnectResponse> {
                override fun onNext(value: ConnectResponse) = Unit

                override fun onError(t: Throwable) {
                    ended.complete(t)
                }

                override fun onCompleted() {
                    ended.completeExceptionally(IllegalStateException("the stream was accepted and completed"))
                }
            }
        val channel = fixtures.channel(agent)
        val stub = AgentServiceGrpc.newStub(channel).withDeadlineAfter(DEADLINE_SECONDS, TimeUnit.SECONDS)
        val requests = stub.connect(observer)
        val error = ended.get(DEADLINE_SECONDS, TimeUnit.SECONDS)
        runCatching { requests.onCompleted() }
        return callRefusalOf(error as StatusRuntimeException)
    }

    fun register(agent: EnrolledAgent) = stub(agent).register(fixtures.registerRequest().build())

    fun refused(block: () -> Unit): CallRefusal = callRefusalOf(assertFailsWith<StatusRuntimeException> { block() })

    fun row(serial: String): Map<String, Any?> =
        jdbc.queryForMap(
            "select predecessor_serial, superseded_at, revoked_at, issued_at, not_after from agent_certificates " +
                "where serial = ?",
            serial,
        )

    fun supersededAt(serial: String): Instant? = row(serial)["superseded_at"]?.let { toInstant(it) }

    fun count(agent: EnrolledAgent): Int =
        jdbc.queryForObject(
            "select count(*) from agent_certificates where agent_id = ?",
            Int::class.java,
            agent.identity.agentId,
        )!!

    fun assertRefusedBy(
        reason: String,
        block: () -> Unit,
    ) = assertEquals(CallRefusal(Status.Code.UNAUTHENTICATED, listOf(reason), listOf("sard.dev")), refused(block))

    fun assertNoNewCertificates(
        agent: EnrolledAgent,
        before: Int,
    ) = assertEquals(before, count(agent))

    /**
     * An agent enrolled at the clock's time T renews: one second before T plus [effective] it is refused as
     * RENEWAL_TOO_FREQUENT, at T plus [effective] it gets a certificate.
     */
    fun assertIntervalBoundary(
        tenant: UUID,
        effective: Duration,
    ) {
        val start = clock.now
        val agent = fixtures.enrolled(tenant)
        clock.now = start + effective - Duration.ofSeconds(1)
        assertEquals(listOf("RENEWAL_TOO_FREQUENT"), refused { renew(agent) }.reasons)
        clock.now = start + effective
        renew(agent)
    }

    companion object {
        fun toInstant(value: Any): Instant =
            when (value) {
                is java.sql.Timestamp -> value.toInstant()
                is java.time.OffsetDateTime -> value.toInstant()
                else -> error("unexpected timestamp type ${value::class}")
            }

        val HOUR: Duration = Duration.ofHours(1)
    }
}
