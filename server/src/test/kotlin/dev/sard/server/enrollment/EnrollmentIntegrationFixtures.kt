// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import com.google.protobuf.ByteString
import dev.sard.proto.agent.v1.EnrollRequest
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.time.Instant
import java.util.UUID
import kotlin.test.assertFailsWith

/** The instant most S2b enrollment integration tests start their clock at. */
internal val ENROLLMENT_NOW: Instant = Instant.parse("2026-09-27T10:00:00Z")

/** The token TTL used by tests that don't care about a particular lifetime. */
internal val ENROLLMENT_TOKEN_TTL: Duration = Duration.ofHours(1)

/** How long a test waits for a racing thread before giving up. */
internal val ENROLLMENT_RACE_WAIT: Duration = Duration.ofSeconds(10)

private const val POLL_MILLIS = 20L

/** A fresh tenant named after [id], as every S2b integration test needs one to enroll into. */
internal fun JdbcTemplate.insertTenant(id: UUID) {
    update("insert into tenants (id, name) values (?, ?)", id, "acme-$id")
}

/** Everything Enroll can have written for [tenant], and the tenant itself. */
internal fun JdbcTemplate.deleteEnrollmentTenantData(tenant: UUID) {
    for (table in listOf("agent_certificates", "enrollment_tokens", "agents")) {
        update("delete from $table where tenant_id = ?", tenant)
    }
    update("delete from tenants where id = ?", tenant)
}

/** Agents of [tenant] right now. */
internal fun JdbcTemplate.countAgents(tenant: UUID): Int? =
    queryForObject("select count(*) from agents where tenant_id = ?", Int::class.java, tenant)

/**
 * Blocks until some other transaction is waiting on the enrollment token's row lock (the
 * signal that a racing Enroll has reached its claim and is parked behind the first).
 */
internal fun JdbcTemplate.awaitEnrollmentRowLockWait(wait: Duration = ENROLLMENT_RACE_WAIT) {
    val sql =
        "select count(*) from pg_stat_activity where wait_event_type = 'Lock' and query like '%enrollment_tokens%'"
    val deadline = System.nanoTime() + wait.toNanos()
    while (queryForObject(sql, Int::class.java) == 0) {
        check(System.nanoTime() < deadline) { "nobody ever waited for the enrollment token's row lock" }
        Thread.sleep(POLL_MILLIS)
    }
}

/** The reason [block] was rejected for, asserting it was rejected at all. */
internal fun rejectionReason(block: () -> Unit): EnrollmentRejectedException.Reason =
    assertFailsWith<EnrollmentRejectedException> { block() }.reason

/** An Enroll request over gRPC for [token] and [csrDer], "db1" by default the hostname (decision 5's shape). */
internal fun enrollRequest(
    token: String,
    csrDer: ByteArray,
    hostname: String = "db1",
): EnrollRequest =
    EnrollRequest
        .newBuilder()
        .setEnrollmentToken(token)
        .setCsrDer(ByteString.copyFrom(csrDer))
        .setHostname(hostname)
        .build()
