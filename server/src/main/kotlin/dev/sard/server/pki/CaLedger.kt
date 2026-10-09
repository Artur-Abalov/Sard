// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

/** Where a CA came from; recorded for every CA the server knows (F4a, Р12). There is no third value. */
enum class CaProvenance { GENERATED, IMPORTED }

/** Whether the CA of the database is already at work, and why (Р11, Р19): the first reason that applies. */
enum class CaUsage {
    /** The step ca is not done and no agent certificate was ever issued: the CA may still be replaced. */
    NONE,

    /** The owner confirmed the CA in the wizard ("onboarding step ca is complete"). */
    STEP_CA_COMPLETE,

    /** The server issued a certificate to an agent of any tenant, revoked or not ("agent certificates issued"). */
    AGENT_CERTIFICATES,
}

/**
 * What the database knows about the CA directory (F4a, Р11, Р12, Р19): the origin of each CA by the fingerprint
 * of its root, and whether a CA is already in use. The CA directory and the database are one installation; the
 * start refuses when they disagree. Calls that cannot reach the database throw, and the start fails with them.
 */
interface CaLedger {
    /** The recorded origin of the CA with this fingerprint, or null when none is recorded. */
    fun provenance(fingerprint: CaFingerprint): CaProvenance?

    /** Records the origin before the CA appears in the CA directory; replaces a record with the same fingerprint. */
    fun record(
        fingerprint: CaFingerprint,
        provenance: CaProvenance,
    )

    fun usage(): CaUsage
}
