// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import org.bouncycastle.asn1.pkcs.PrivateKeyInfo
import org.bouncycastle.openssl.jcajce.JcaPEMKeyConverter
import java.io.IOException
import java.nio.ByteBuffer
import java.nio.channels.FileChannel
import java.nio.file.FileSystemException
import java.nio.file.Files
import java.nio.file.NoSuchFileException
import java.nio.file.Path
import java.nio.file.StandardCopyOption.ATOMIC_MOVE
import java.nio.file.StandardOpenOption.CREATE_NEW
import java.nio.file.StandardOpenOption.READ
import java.nio.file.StandardOpenOption.WRITE
import java.nio.file.attribute.PosixFilePermission
import java.nio.file.attribute.PosixFilePermission.OWNER_EXECUTE
import java.nio.file.attribute.PosixFilePermission.OWNER_READ
import java.nio.file.attribute.PosixFilePermission.OWNER_WRITE
import java.nio.file.attribute.PosixFilePermissions
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.time.Clock
import java.time.Duration
import java.time.Instant
import java.util.UUID

/** The layout of the CA directory, shared with [CaImportSource], which reads the same layout. */
internal const val CA = "ca"
internal const val CERT = "ca.crt"
internal const val KEY = "ca.key"
private val OWNER = setOf(OWNER_READ, OWNER_WRITE, OWNER_EXECUTE)
private val OWNER_DIR = PosixFilePermissions.asFileAttribute(OWNER)
private val OWNER_FILE = PosixFilePermissions.asFileAttribute(setOf(OWNER_READ, OWNER_WRITE))
private const val STAGING = ".tmp-"
private const val REPLACED = ".tmp-replaced-"
private const val TOGETHER =
    "server volumes are reinstalled together (the database and the CA directory); see docs/operator/09-troubleshooting.md"
private const val STEP_CA_COMPLETE = "onboarding step ca is complete"
private const val AGENT_CERTIFICATES = "agent certificates issued"

/** A staging directory this old was left by a crashed first start, not by one still running. */
private val STALE_STAGING = Duration.ofHours(1)

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
    private val clock: Clock,
    private val ledger: CaLedger,
    private val writeFile: (Path, String) -> Unit = ::write,
) {
    private val ca = dir.resolve(CA)

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
        writing(source) {
            if (Files.notExists(dir)) Files.createDirectories(dir, OWNER_DIR)
        }
        requireOwnerOnly(dir)
        writing(source) {
            restoreInterruptedReplacement()
            removeStaleStaging()
        }
        return if (Files.exists(ca)) present(source) else empty(source, generate)
    }

    private fun present(source: CaImport?): OpenedCa {
        val pair = load()
        val fingerprint = CaFingerprint.of(pair.certificate)
        val provenance = ledger.provenance(fingerprint) ?: throw notRecorded(fingerprint)
        val replacement = source?.reconcile(fingerprint, ledger.usage())
        if (replacement == null) return OpenedCa(pair, CaOrigin.EXISTING, provenance)
        val imported = CaKeyPair(replacement.certificate, replacement.key)
        ledger.record(CaFingerprint.of(imported.certificate), CaProvenance.IMPORTED)
        writing(source) { replace(imported) }
        return OpenedCa(load(), CaOrigin.IMPORTED, CaProvenance.IMPORTED, replaced = fingerprint)
    }

    private fun empty(
        source: CaImport?,
        generate: () -> CaKeyPair,
    ): OpenedCa {
        val usage = ledger.usage()
        if (source == null && usage != CaUsage.NONE) throw missing(usage)
        val pair = source?.read()?.let { CaKeyPair(it.certificate, it.key) } ?: generate()
        val provenance = if (source == null) CaProvenance.GENERATED else CaProvenance.IMPORTED
        ledger.record(CaFingerprint.of(pair.certificate), provenance)
        val published = writing(source) { publish(pair) }
        val loaded = load()
        // The start that lost the race works with the winner's CA, whose origin the winner recorded before it published.
        val recorded = ledger.provenance(CaFingerprint.of(loaded.certificate)) ?: throw notRecorded(CaFingerprint.of(loaded.certificate))
        return OpenedCa(loaded, originOf(published, source), recorded)
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
            "the CA ${fingerprint.hex} in $ca: CA origin is not recorded in the database; " + TOGETHER,
        )

    private fun missing(usage: CaUsage): CaStartRefused {
        val why = if (usage == CaUsage.STEP_CA_COMPLETE) STEP_CA_COMPLETE else AGENT_CERTIFICATES
        return CaStartRefused(
            CaStartRefusal.CA_MISSING,
            "CA directory is empty: no CA in $ca, and the database says the CA is in use ($why); " +
                "set SARD_PKI_IMPORT_DIR to the backup of the CA to bring it back; " + TOGETHER,
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

    /** True when this call published [pair], false when another start did first. */
    private fun publish(pair: CaKeyPair): Boolean {
        val staging = stage(pair)
        try {
            Files.move(staging, ca, ATOMIC_MOVE)
            syncDirectory()
            return true
        } catch (e: FileSystemException) {
            if (Files.notExists(ca)) throw e
            return false
        } finally {
            staging.toFile().deleteRecursively()
        }
    }

    /** A staging directory with the whole [pair] in it; nothing of it is left behind when writing fails. */
    private fun stage(pair: CaKeyPair): Path {
        val staging = Files.createTempDirectory(dir, STAGING, OWNER_DIR)
        try {
            writeFile(staging.resolve(KEY), Pem.privateKey(pair.privateKey))
            writeFile(staging.resolve(CERT), Pem.certificate(pair.certificate))
        } catch (e: IOException) {
            staging.toFile().deleteRecursively()
            throw e
        }
        return staging
    }

    /**
     * Swaps the CA in place for [pair] (Р11): the new one is written whole in a staging directory, the present one
     * is renamed away, the new one renamed in, the old one deleted. A start that dies between the renames finds
     * the old CA under its new name and puts it back ([restoreInterruptedReplacement]).
     */
    private fun replace(pair: CaKeyPair) {
        val staging = stage(pair)
        val old = dir.resolve(REPLACED + UUID.randomUUID())
        try {
            Files.move(ca, old, ATOMIC_MOVE)
            Files.move(staging, ca, ATOMIC_MOVE)
        } catch (e: IOException) {
            if (Files.notExists(ca) && Files.exists(old)) Files.move(old, ca, ATOMIC_MOVE)
            staging.toFile().deleteRecursively()
            throw e
        }
        syncDirectory()
        old.toFile().deleteRecursively()
    }

    /** No `ca` but the CA that was swapped away: the replacement died half way, the old CA is the CA. */
    private fun restoreInterruptedReplacement() {
        val swapped = list { it.fileName.toString().startsWith(REPLACED) }
        if (Files.notExists(ca)) swapped.firstOrNull()?.let { Files.move(it, ca, ATOMIC_MOVE) }
        list { it.fileName.toString().startsWith(REPLACED) }.forEach { it.toFile().deleteRecursively() }
    }

    private fun list(filter: (Path) -> Boolean): List<Path> = Files.list(dir).use { entries -> entries.filter(filter).toList() }

    private fun removeStaleStaging() {
        val cutoff = clock.instant() - STALE_STAGING
        Files.list(dir).use { entries ->
            entries
                .filter { it.fileName.toString().startsWith(STAGING) }
                .filter { isStale(it, cutoff) }
                .forEach { it.toFile().deleteRecursively() }
        }
    }

    /** Gone already (a concurrent start published or removed it): not ours to clean. Other failures stop the start. */
    private fun isStale(
        path: Path,
        cutoff: Instant,
    ): Boolean =
        try {
            Files.getLastModifiedTime(path).toInstant() < cutoff
        } catch (_: NoSuchFileException) {
            false
        }

    /** Makes the rename durable; not observable without a crash, so no test covers it. */
    private fun syncDirectory() { // mutflow:falsePositive fsync changes nothing observable without an OS crash
        val channel = FileChannel.open(dir, READ)
        try {
            channel.force(true)
        } finally {
            channel.close()
        }
    }

    private fun load(): CaKeyPair {
        val key = ca.resolve(KEY)
        requireOwnerOnly(ca)
        requireOwnerOnly(key)
        requireOwnerOnly(ca.resolve(CERT))
        val certificate =
            Files.newInputStream(ca.resolve(CERT)).use {
                CertificateFactory.getInstance("X.509").generateCertificate(it) as X509Certificate
            }
        val der = Pem.decode("PRIVATE KEY", Files.readString(key))
        return CaKeyPair(certificate, JcaPEMKeyConverter().getPrivateKey(PrivateKeyInfo.getInstance(der)))
    }

    private fun requireOwnerOnly(path: Path) {
        ownerOnlyViolation(path)?.let { throw InsecureKeyStorageException(it) }
    }
}

private fun write(
    path: Path,
    text: String,
) {
    FileChannel.open(path, setOf(CREATE_NEW, WRITE), OWNER_FILE).use {
        it.write(ByteBuffer.wrap(text.toByteArray()))
        it.force(true)
    }
}
