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

private const val CA = "ca"
private const val CERT = "ca.crt"
private const val KEY = "ca.key"

/** The two things a CA directory needs from a source of a CA to import (ADR 0052). */
interface CaImport {
    /** The CA to take, or throws [CaImportRefused] for the first check it fails. */
    fun read(): ImportedCa

    /** A CA is already in the CA directory: the same one is fine, another one stops the start. */
    fun reconcile(present: CaFingerprint)

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
    private val paths = listOf(root, caDir, certPath, keyPath)

    /** The CA of the source, or throws [CaImportRefused] for the first check it fails. */
    override fun read(): ImportedCa {
        requireDirectory()
        requireFile(certPath)
        requireFile(keyPath)
        requireReadable()
        requireOwnerOnly()
        val certificate = content.certificate(text(certPath))
        val key = content.key(text(keyPath))
        content.requireMatch(certificate, key)
        profile.requireUsableCa(certificate, clock.instant())
        warnIfExpiresSoon(certificate)
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

    private fun text(path: Path): String =
        try {
            String(Files.readAllBytes(path), Charsets.ISO_8859_1)
        } catch (_: IOException) {
            throw refusal(CaImportRefusal.IMPORT_FILE_UNREADABLE, "$path cannot be read by the server")
        }

    /**
     * A CA is already in the CA directory. The same one as in the source: nothing to import. Another one: the
     * server does not start. A source that cannot be read: the setting is stale, the server starts and says so.
     */
    override fun reconcile(present: CaFingerprint) {
        val theirs = runCatching { CaFingerprint.of(content.certificate(text(certPath))) }.getOrNull()
        when {
            theirs == null -> {
                log.warn(
                    "CA import skipped: a CA is already present and SARD_PKI_IMPORT_DIR={} cannot be read; " +
                        "remove SARD_PKI_IMPORT_DIR",
                    root,
                )
            }

            theirs == present -> {
                log.info("CA import not needed: the CA {} is already present", present.hex)
            }

            else -> {
                throw refusal(
                    CaImportRefusal.CA_ALREADY_PRESENT,
                    "the CA directory holds ${present.hex}, the source holds ${theirs.hex}",
                )
            }
        }
    }

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

    private fun requireDirectory() {
        if (!Files.isDirectory(root)) {
            throw refusal(CaImportRefusal.IMPORT_SOURCE_MISSING, "$root does not exist or is not a directory")
        }
    }

    private fun requireReadable() {
        val unreadable = paths.firstOrNull { !isReadable(it) } ?: return
        throw refusal(CaImportRefusal.IMPORT_FILE_UNREADABLE, "$unreadable cannot be read by the server")
    }

    private fun requireOwnerOnly() {
        for (path in paths) {
            ownerOnlyViolation(path)?.let { throw refusal(CaImportRefusal.IMPORT_PERMISSIONS_TOO_OPEN, it) }
        }
    }

    private fun requireFile(path: Path) {
        if (!Files.isRegularFile(path)) throw refusal(CaImportRefusal.IMPORT_FILE_MISSING, "expected $path")
    }

    private fun refusal(
        reason: CaImportRefusal,
        detail: String,
    ): CaImportRefused = CaImportRefused(reason, detail, root)
}
