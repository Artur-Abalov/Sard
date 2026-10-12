// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import org.springframework.dao.DataAccessResourceFailureException

/** The ledger in memory: records are kept, [usage] is what the test says, [down] makes the database unreachable. */
class FakeCaLedger(
    var usage: CaUsage = CaUsage.NONE,
    var down: Boolean = false,
    /** Every CA is known as generated, for tests that open a CA directory again with a ledger of their own. */
    private val permissive: Boolean = false,
) : CaLedger {
    val recorded = java.util.concurrent.ConcurrentHashMap<CaFingerprint, CaProvenance>()

    /** Called with the fingerprint and the origin just before a record is kept: a test looks at the directory. */
    var onRecord: (CaFingerprint, CaProvenance) -> Unit = { _, _ -> }
    var failRecording = false

    private fun alive() {
        if (down) throw DataAccessResourceFailureException("database is down")
    }

    override fun provenance(fingerprint: CaFingerprint): CaProvenance? {
        alive()
        return recorded[fingerprint] ?: CaProvenance.GENERATED.takeIf { permissive }
    }

    override fun record(
        fingerprint: CaFingerprint,
        provenance: CaProvenance,
    ) {
        alive()
        onRecord(fingerprint, provenance)
        if (failRecording) throw DataAccessResourceFailureException("cannot write")
        recorded[fingerprint] = provenance
    }

    override fun usage(): CaUsage {
        alive()
        return usage
    }
}
