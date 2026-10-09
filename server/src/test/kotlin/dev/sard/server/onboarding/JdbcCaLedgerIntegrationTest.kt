// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.CaFingerprint
import dev.sard.server.pki.CaProvenance
import dev.sard.server.pki.CaUsage
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private val NOW: Instant = Instant.parse("2026-10-09T12:00:00Z")
private val G = CaFingerprint("a".repeat(64))
private val F = CaFingerprint("b".repeat(64))
private val TENANT = UUID.fromString("00000000-0000-0000-0000-000000000001")
private val OTHER_TENANT = UUID.fromString("00000000-0000-0000-0000-0000000000b2")

/** What the database knows about the CA (migration V202610091200, Р11, Р12, Р19). */
@SpringBootTest(properties = ["spring.grpc.server.port=0", "server.port=0"])
@Import(TestcontainersConfiguration::class)
class JdbcCaLedgerIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val ledger = JdbcCaLedger(jdbc, java.time.Clock.fixed(NOW, java.time.ZoneOffset.UTC))

    @AfterTest
    fun `forget the state`() {
        jdbc.update("delete from agent_certificates where serial = ?", "a".repeat(32))
        jdbc.update("delete from agents where hostname = 'a'")
        jdbc.update("delete from tenants where id = ?", OTHER_TENANT)
        jdbc.update("delete from onboarding_steps")
        jdbc.update("delete from ca_origins")
    }

    private fun certificateOfTenant(tenant: UUID) {
        if (tenant == OTHER_TENANT) jdbc.update("insert into tenants (id, name) values (?, 'B')", tenant)
        val agent = UUID.randomUUID()
        jdbc.update(
            "insert into agents (id, tenant_id, hostname, registered_at) values (?, ?, 'a', ?)",
            agent,
            tenant,
            Timestamp.from(NOW),
        )
        jdbc.update(
            "insert into agent_certificates (serial, tenant_id, agent_id, issued_at, not_after, revoked_at) " +
                "values (?, ?, ?, ?, ?, ?)",
            "a".repeat(32),
            tenant,
            agent,
            Timestamp.from(NOW),
            Timestamp.from(NOW.plusSeconds(60)),
            Timestamp.from(NOW),
        )
    }

    @Test
    fun `Происхождения CA нет, пока оно не записано`() {
        assertNull(ledger.provenance(G))
    }

    @Test
    fun `Записанное происхождение читается по отпечатку`() {
        ledger.record(G, CaProvenance.GENERATED)
        ledger.record(F, CaProvenance.IMPORTED)

        assertEquals(CaProvenance.GENERATED, ledger.provenance(G))
        assertEquals(CaProvenance.IMPORTED, ledger.provenance(F))
    }

    @Test
    fun `Запись с тем же отпечатком заменяется`() {
        ledger.record(G, CaProvenance.GENERATED)
        ledger.record(G, CaProvenance.IMPORTED)

        assertEquals(CaProvenance.IMPORTED, ledger.provenance(G))
        assertEquals(1, jdbc.queryForObject("select count(*) from ca_origins", Int::class.java))
    }

    @Test
    fun `CA не в деле, пока нет ни шага ca, ни сертификатов агентов`() {
        ledger.record(G, CaProvenance.GENERATED)

        assertEquals(CaUsage.NONE, ledger.usage())
    }

    @Test
    fun `Шаг ca выполнен — CA в деле`() {
        JdbcOnboardingSteps(jdbc).confirmCa(NOW)

        assertEquals(CaUsage.STEP_CA_COMPLETE, ledger.usage())
    }

    @Test
    fun `Любой сертификат агента, и отозванный тоже, и другого тенанта — CA в деле`() {
        certificateOfTenant(OTHER_TENANT)

        assertEquals(CaUsage.AGENT_CERTIFICATES, ledger.usage())
    }

    @Test
    fun `Шаг ca называется раньше сертификатов`() {
        certificateOfTenant(TENANT)
        JdbcOnboardingSteps(jdbc).confirmCa(NOW)

        assertEquals(CaUsage.STEP_CA_COMPLETE, ledger.usage())
    }
}
