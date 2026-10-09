// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.pki.CaFingerprint
import dev.sard.server.pki.CaLedger
import dev.sard.server.pki.CaProvenance
import dev.sard.server.pki.CaUsage
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Clock

/**
 * The ledger in the database (migration V202610091200): the origins by the fingerprint of the root, and the two
 * facts that put a CA to work, the confirmed step ca and any agent certificate of any tenant.
 */
class JdbcCaLedger(
    private val jdbc: JdbcTemplate,
    private val clock: Clock,
    private val steps: OnboardingSteps,
) : CaLedger {
    override fun provenance(fingerprint: CaFingerprint): CaProvenance? =
        jdbc
            .queryForList("select origin from ca_origins where fingerprint = ?", String::class.java, fingerprint.hex)
            .firstOrNull()
            ?.let { CaProvenance.valueOf(it.uppercase()) }

    override fun record(
        fingerprint: CaFingerprint,
        provenance: CaProvenance,
    ) {
        jdbc.update(
            "insert into ca_origins (fingerprint, origin, recorded_at) values (?, ?, ?) " +
                "on conflict (fingerprint) do update set origin = excluded.origin, recorded_at = excluded.recorded_at",
            fingerprint.hex,
            provenance.name.lowercase(),
            Timestamp.from(clock.instant()),
        )
    }

    override fun usage(): CaUsage =
        when {
            steps.caConfirmed() -> CaUsage.STEP_CA_COMPLETE
            exists("select 1 from agent_certificates") -> CaUsage.AGENT_CERTIFICATES
            else -> CaUsage.NONE
        }

    private fun exists(sql: String): Boolean = jdbc.queryForList("$sql limit 1").isNotEmpty()
}
