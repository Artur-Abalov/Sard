// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.io.IOException
import java.nio.file.Files
import java.nio.file.Path
import java.nio.file.attribute.PosixFilePermission
import java.nio.file.attribute.PosixFilePermission.OWNER_EXECUTE
import java.nio.file.attribute.PosixFilePermission.OWNER_READ
import java.nio.file.attribute.PosixFilePermission.OWNER_WRITE
import java.nio.file.attribute.PosixFilePermissions
import java.time.Clock

/** The layout of the CA directory, shared with [CaImportSource], which reads the same layout. */
internal const val CA = "ca"
internal const val CERT = "ca.crt"
internal const val KEY = "ca.key"
internal val OWNER = setOf(OWNER_READ, OWNER_WRITE, OWNER_EXECUTE)
private const val TOGETHER =
    "server volumes are reinstalled together (the database and the CA directory); " +
        "see docs/operator/09-troubleshooting.md"
private const val STEP_CA_COMPLETE = "onboarding step ca is complete"
private const val AGENT_CERTIFICATES = "agent certificates issued"

/** A key file or directory that someone besides its owner may access. */
class InsecureKeyStorageException(
    message: String,
) : RuntimeException(message)

/** Describes why [path] is accessible beyond its owner, or null when it is not. POSIX only. */
fun ownerOnlyViolation(path: Path): String? {
    val permissions: Set<PosixFilePermission> = Files.getPosixFilePermissions(path)
    return if ((permissions - OWNER).isEmpty()) {
        null
    } else {
        "$path is accessible beyond its owner (${PosixFilePermissions.toString(permissions)})"
    }
}

/** Where the CA of a start came from; the server logs it with the fingerprint at every start (ADR 0052). */
enum class CaOrigin { GENERATED, IMPORTED, EXISTING }

/**
 * The CA a start works with: [origin] is where it came from at this start (what the log says), [provenance] is
 * what the database has recorded about it for good; [replaced] is the generated or imported CA this start
 * swapped it for (F4a, Р11), if any.
 */
class OpenedCa(
    val pair: CaKeyPair,
    val origin: CaOrigin,
    val provenance: CaProvenance,
    val replaced: CaFingerprint? = null,
)

/**
 * `<dir>/ca/{ca.crt,ca.key}`, owner-only. The `ca` directory appears by one atomic
 * rename, so concurrent first starts end up with the same CA: the loser loads the winner's.
 *
 * The CA directory and the database are one installation (F4a, Р19): [ledger] says where each CA came from
 * and whether a CA is in use. The origin of a CA is recorded before the CA appears here, so a CA in this
 * directory always has one; a CA without one, or no CA where the database has one in use, stops the start.
 */
class CaDirectory(
    private val dir: Path,
    clock: Clock,
    private val ledger: CaLedger,
    writeFile: (Path, String) -> Unit = ::write,
) {
    private val store = CaStore(dir, clock, writeFile)

    fun loadOrCreate(generate: () -> CaKeyPair): CaKeyPair = open(null, generate).pair

    /**
     * The CA of this directory. Empty directory: the CA of [source] when there is one (ADR 0052), else a
     * generated one, unless the database has a CA in use and no [source] brings it back (CA_MISSING). A CA
     * that is there stays: a [source] is judged against it (see [CaImport.reconcile]) and may replace it while
     * the CA is not in use (Р11).
     */
    fun open(
        source: CaImport?,
        generate: () -> CaKeyPair,
    ): OpenedCa {
        writing(source) { store.createDirectory() }
        store.requireDirectoryOwnerOnly()
        writing(source) { store.tidy() }
        return if (store.hasCa()) present(source) else empty(source, generate)
    }

    private fun present(source: CaImport?): OpenedCa {
        val pair = store.load()
        val fingerprint = CaFingerprint.of(pair.certificate)
        val provenance = recordedFor(fingerprint)
        val replacement = source?.reconcile(fingerprint, ledger.usage())
        if (replacement == null) return OpenedCa(pair, CaOrigin.EXISTING, provenance)
        val imported = CaKeyPair(replacement.certificate, replacement.key)
        ledger.record(CaFingerprint.of(imported.certificate), CaProvenance.IMPORTED)
        writing(source) { store.replace(imported) }
        return OpenedCa(store.load(), CaOrigin.IMPORTED, CaProvenance.IMPORTED, replaced = fingerprint)
    }

    private fun empty(
        source: CaImport?,
        generate: () -> CaKeyPair,
    ): OpenedCa {
        val usage = ledger.usage()
        if (source == null && usage != CaUsage.NONE) throw missing(usage)
        val pair = newCa(source, generate)
        ledger.record(CaFingerprint.of(pair.certificate), provenanceOf(source))
        val published = writing(source) { store.publish(pair) }
        val loaded = store.load()
        // The start that lost the race works with the winner's CA, whose origin the winner recorded before
        // it published.
        return OpenedCa(loaded, originOf(published, source), recordedFor(CaFingerprint.of(loaded.certificate)))
    }

    /** The CA of the [source] when there is one, else a generated one. */
    private fun newCa(
        source: CaImport?,
        generate: () -> CaKeyPair,
    ): CaKeyPair = source?.read()?.let { CaKeyPair(it.certificate, it.key) } ?: generate()

    private fun provenanceOf(source: CaImport?) = if (source == null) CaProvenance.GENERATED else CaProvenance.IMPORTED

    private fun recordedFor(fingerprint: CaFingerprint): CaProvenance {
        val recorded = ledger.provenance(fingerprint)
        return recorded ?: throw notRecorded(fingerprint)
    }

    private fun originOf(
        published: Boolean,
        source: CaImport?,
    ) = when {
        !published -> CaOrigin.EXISTING
        source == null -> CaOrigin.GENERATED
        else -> CaOrigin.IMPORTED
    }

    private fun notRecorded(fingerprint: CaFingerprint) =
        CaStartRefused(
            CaStartRefusal.CA_ORIGIN_NOT_RECORDED,
            "the CA ${fingerprint.hex} in ${store.caPath}: CA origin is not recorded in the database; $TOGETHER",
        )

    private fun missing(usage: CaUsage): CaStartRefused {
        val why = if (usage == CaUsage.STEP_CA_COMPLETE) STEP_CA_COMPLETE else AGENT_CERTIFICATES
        return CaStartRefused(
            CaStartRefusal.CA_MISSING,
            "CA directory is empty: no CA in ${store.caPath}, and the database says the CA is in use ($why); " +
                "set SARD_PKI_IMPORT_DIR to the backup of the CA to bring it back; $TOGETHER",
        )
    }

    /** A failed write while importing is a refusal that names the directory; otherwise the failure is passed on. */
    private fun <T> writing(
        source: CaImport?,
        action: () -> T,
    ): T =
        try {
            action()
        } catch (e: IOException) {
            throw source?.writeFailed(dir, e) ?: e
        }
}
