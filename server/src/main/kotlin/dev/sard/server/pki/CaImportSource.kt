// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import org.slf4j.LoggerFactory
import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path
import java.security.PrivateKey
import java.security.cert.X509Certificate
import java.time.Clock
import java.time.Duration

private val log = LoggerFactory.getLogger(CaImportSource::class.java)

/** A CA with this much or less left is imported with a warning (ADR 0052). */
private val WARN_WITHIN = Duration.ofDays(90)

/** The two things a CA directory needs from a source of a CA to import (ADR 0052). */
interface CaImport {
    /** The CA to take, or throws [CaImportRefused] for the first check it fails. */
    fun read(): ImportedCa

    /**
     * A CA is already in the CA directory, and the database says how far it is in use ([usage], Р11, Р18). The
     * CA to put in its place, or null when the directory keeps its CA:
     * - [CaUsage.STEP_CA_COMPLETE]: the same CA is fine, another one stops the start (CA_ALREADY_PRESENT), a
     *   source that cannot be read only warns (F8 Р3);
     * - otherwise the source must pass every check, whatever it holds (Р18); the same CA is fine, another one
     *   replaces the present one when no certificate was issued ([CaUsage.NONE]) and stops the start otherwise.
     */
    fun reconcile(
        present: CaFingerprint,
        usage: CaUsage,
    ): ImportedCa?

    /** The refusal for a CA directory that could not be written. */
    fun writeFailed(
        caDirectory: Path,
        cause: IOException,
    ): CaImportRefused
}

/** A certificate and key read from a source; [CaKeyPair] checks that they belong together. */
class ImportedCa(
    val certificate: X509Certificate,
    internal val key: PrivateKey,
) {
    /** Never prints the key. */
    override fun toString() = "ImportedCa(${certificate.subjectX500Principal})"
}

/**
 * The CA to import: `<root>/ca/{ca.crt,ca.key}`, the layout of the CA directory (ADR 0052). Read only; nothing
 * in [root] is ever modified. Only these two files are read.
 */
class CaImportSource(
    private val root: Path,
    private val clock: Clock,
    private val isReadable: (Path) -> Boolean = Files::isReadable,
) : CaImport {
    private val caDir = root.resolve(CA)
    private val certPath = caDir.resolve(CERT)
    private val keyPath = caDir.resolve(KEY)
    private val content = CaImportContent(root, certPath, keyPath)
    private val profile = CaImportProfile(root, certPath)
    private val files = CaImportFiles(root, certPath, keyPath, isReadable)

    /** The CA of the source, or throws [CaImportRefused] for the first check it fails. */
    override fun read(): ImportedCa = load().also { warnIfExpiresSoon(it.certificate) }

    /** Every check of the table, no warning: for a source that is not about to be imported. */
    private fun load(): ImportedCa {
        files.requireUsable()
        val certificate = content.certificate(files.text(certPath))
        val key = content.key(files.text(keyPath))
        content.requireMatch(certificate, key)
        profile.requireUsableCa(certificate, clock.instant())
        return ImportedCa(certificate, key)
    }

    private fun warnIfExpiresSoon(certificate: X509Certificate) {
        val notAfter = certificate.notAfter.toInstant()
        val left = Duration.between(clock.instant(), notAfter)
        if (left <= WARN_WITHIN) {
            val days = left.toDays()
            log.warn("CA expires {}, {} {} left", notAfter, days, if (days == 1L) "day" else "days")
        }
    }

    override fun reconcile(
        present: CaFingerprint,
        usage: CaUsage,
    ): ImportedCa? = if (usage == CaUsage.STEP_CA_COMPLETE) skipping(present) else judging(present, usage)

    /** The step ca is done: the same CA is fine, another stops the start, a source that is gone is stale. */
    private fun skipping(present: CaFingerprint): ImportedCa? {
        val theirs = runCatching { CaFingerprint.of(content.certificate(files.text(certPath))) }.getOrNull()
        when {
            theirs == null -> {
                log.warn(
                    "CA import skipped: a CA is already present and SARD_PKI_IMPORT_DIR={} cannot be read; " +
                        "remove SARD_PKI_IMPORT_DIR",
                    root,
                )
            }

            theirs == present -> {
                notNeeded(present)
            }

            else -> {
                throw alreadyPresent(present, theirs, CaUsage.STEP_CA_COMPLETE)
            }
        }
        return null
    }

    /** The step ca is open: the source is checked in full first, a CA that did nothing yet may be replaced. */
    private fun judging(
        present: CaFingerprint,
        usage: CaUsage,
    ): ImportedCa? {
        val source = load()
        val theirs = CaFingerprint.of(source.certificate)
        return when {
            theirs == present -> {
                notNeeded(present)
                null
            }

            usage != CaUsage.NONE -> {
                throw alreadyPresent(present, theirs, usage)
            }

            else -> {
                source.also { warnIfExpiresSoon(it.certificate) }
            }
        }
    }

    private fun notNeeded(present: CaFingerprint) {
        log.info("CA import not needed: the CA {} is already present", present.hex)
    }

    private fun alreadyPresent(
        present: CaFingerprint,
        theirs: CaFingerprint,
        usage: CaUsage,
    ): CaImportRefused =
        refusal(
            CaImportRefusal.CA_ALREADY_PRESENT,
            "the CA directory holds ${present.hex}, the source holds ${theirs.hex}: ${usage.reason()}, so the CA cannot be " +
                "replaced; to move a server see docs/operator/08-migrate-and-remove.md",
        )

    /** The CA directory could not be written; the refusal names it, never a file's content. */
    override fun writeFailed(
        caDirectory: Path,
        cause: IOException,
    ): CaImportRefused =
        CaImportRefused(
            CaImportRefusal.IMPORT_WRITE_FAILED,
            "cannot write to the CA directory $caDirectory: ${cause.message}",
            root,
        )

    private fun refusal(
        reason: CaImportRefusal,
        detail: String,
    ): CaImportRefused = CaImportRefused(reason, detail, root)
}
