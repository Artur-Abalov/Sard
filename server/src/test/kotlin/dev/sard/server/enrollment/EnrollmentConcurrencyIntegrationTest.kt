// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.EnrollmentRejectedException.Reason
import dev.sard.server.pki.MovableClock
import dev.sard.server.pki.PkiFixtures.resource
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.util.UUID
import java.util.concurrent.Callable
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.coroutines.cancellation.CancellationException
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

/**
 * Rules "Из одновременных регистраций с одним токеном успешна ровно одна", "Отозвать можно любой
 * ещё не использованный токен" (the race with a concurrent revoke) and "Неудачная попытка не
 * расходует токен" (cancellation before and after commit, decision 7).
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, EnrollmentTestConfiguration::class)
class EnrollmentConcurrencyIntegrationTest(
    @Autowired private val enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val ca: GatedCertificateAuthority,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val acme = UUID.randomUUID()
    private val csr = resource("agent-p256.csr")

    @BeforeTest
    fun `start at a known instant with a fresh tenant`() {
        clock.now = ENROLLMENT_NOW
        ca.gate = null
        ca.entered.drainPermits()
        jdbc.insertTenant(acme)
    }

    @AfterTest
    fun `drop everything of the tenant`() {
        jdbc.deleteEnrollmentTenantData(acme)
    }

    private fun newToken() = tokens.create(acme, ENROLLMENT_TOKEN_TTL)

    private fun countAgents(): Int? = jdbc.countAgents(acme)

    @Test
    fun `Вторая одновременная регистрация с тем же токеном получает TOKEN_USED`() {
        val token = newToken().reveal()
        ca.gate = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)
        try {
            val first = pool.submit(Callable { runCatching { enrollment.enroll(token, csr, "first") } })
            val entered = ca.entered.tryAcquire(ENROLLMENT_RACE_WAIT.toSeconds(), TimeUnit.SECONDS)
            assertTrue(entered, "the first never reached the CA")
            val secondCsr = resource("agent-p384.csr")
            val second = pool.submit(Callable { runCatching { enrollment.enroll(token, secondCsr, "second") } })
            jdbc.awaitEnrollmentRowLockWait()
            ca.gate?.countDown()
            val results = listOf(first.get(), second.get())

            assertTrue(results[0].isSuccess, "the first holds the token: ${results[0]}")
            val loser = results[1].exceptionOrNull()
            assertEquals(Reason.TOKEN_USED, (loser as EnrollmentRejectedException).reason)
            assertEquals(1, countAgents())
        } finally {
            ca.gate?.countDown()
            pool.shutdownNow()
        }
    }

    @Test
    fun `Из десяти одновременных регистраций с одним токеном успешна одна`() {
        val token = newToken().reveal()
        val start = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(RACERS)
        try {
            val futures =
                (1..RACERS).map { i ->
                    pool.submit(
                        Callable {
                            start.await(ENROLLMENT_RACE_WAIT.toSeconds(), TimeUnit.SECONDS)
                            runCatching { enrollment.enroll(token, csr, "racer-$i") }
                        },
                    )
                }
            start.countDown()
            val results = futures.map { it.get(ENROLLMENT_RACE_WAIT.toSeconds(), TimeUnit.SECONDS) }

            assertEquals(1, results.count { it.isSuccess })
            val losers = results.filter { it.isFailure }
            assertEquals(RACERS - 1, losers.size)
            for (loser in losers) {
                assertEquals(Reason.TOKEN_USED, (loser.exceptionOrNull() as EnrollmentRejectedException).reason)
            }
            assertEquals(1, countAgents())
        } finally {
            pool.shutdownNow()
        }
    }

    @Test
    fun `Отзыв во время регистрации отклоняется после её успеха`() {
        val issued = newToken()
        ca.gate = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(2)
        try {
            val registration = pool.submit(Callable { runCatching { enrollment.enroll(issued.reveal(), csr, "db1") } })
            val started = ca.entered.tryAcquire(ENROLLMENT_RACE_WAIT.toSeconds(), TimeUnit.SECONDS)
            assertTrue(started, "the registration never reached the CA")
            val revocation = pool.submit(Callable { tokens.revoke(acme, issued.id, clock.now) })
            jdbc.awaitEnrollmentRowLockWait()
            ca.gate?.countDown()

            val succeeded = registration.get(ENROLLMENT_RACE_WAIT.toSeconds(), TimeUnit.SECONDS).isSuccess
            assertTrue(succeeded, "the registration must succeed")
            val revoked = revocation.get(ENROLLMENT_RACE_WAIT.toSeconds(), TimeUnit.SECONDS)
            assertEquals(RevokeResult.Rejected(RevokeRejection.USED), revoked)
            assertEquals(EnrollmentTokenState.USED, tokens.get(acme, issued.id)?.state)
        } finally {
            ca.gate?.countDown()
            pool.shutdownNow()
        }
    }

    @Test
    fun `Регистрация, прерванная агентом до фиксации, не расходует токен`() {
        val issued = newToken()
        ca.gate = CountDownLatch(1)
        val pool = Executors.newFixedThreadPool(1)
        val cancelled =
            java.util.concurrent.atomic
                .AtomicBoolean(false)
        try {
            val registration =
                pool.submit(
                    Callable {
                        runCatching { enrollment.enroll(issued.reveal(), csr, "db1") { cancelled.get() } }
                    },
                )
            val started = ca.entered.tryAcquire(ENROLLMENT_RACE_WAIT.toSeconds(), TimeUnit.SECONDS)
            assertTrue(started, "the registration never reached the CA")
            cancelled.set(true)
            ca.gate?.countDown()
            val result = registration.get(ENROLLMENT_RACE_WAIT.toSeconds(), TimeUnit.SECONDS)

            assertTrue(result.isFailure, "a cancelled registration must not succeed")
            assertTrue(result.exceptionOrNull() is CancellationException, "$result")
            assertEquals(0, countAgents())
            assertEquals(EnrollmentTokenState.ACTIVE, tokens.get(acme, issued.id)?.state)
        } finally {
            ca.gate?.countDown()
            pool.shutdownNow()
        }
    }

    @Test
    fun `Обрыв после фиксации расходует токен и оставляет агента`() {
        // Decision 7: the only point that reads `cancelled` is before the commit (decision 7's
        // "before"). Once it has passed, as here, nothing later — including the response never
        // reaching the agent — rolls back what already committed.
        val issued = newToken()
        val enrolled = enrollment.enroll(issued.reveal(), csr, "db1") { false }
        val row = jdbc.queryForMap("select used_at, agent_id from enrollment_tokens where id = ?", issued.id)
        assertEquals(enrolled.agentId, row["agent_id"])
        assertEquals(1, countAgents())
        assertEquals(EnrollmentTokenState.USED, tokens.get(acme, issued.id)?.state)
    }

    private companion object {
        const val RACERS = 10
    }
}
