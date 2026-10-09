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

/** The layout of the CA directory, shared with [CaImportSource], which reads the same layout. */
internal const val CA = "ca"
internal const val CERT = "ca.crt"
internal const val KEY = "ca.key"
private val OWNER = setOf(OWNER_READ, OWNER_WRITE, OWNER_EXECUTE)
private val OWNER_DIR = PosixFilePermissions.asFileAttribute(OWNER)
private val OWNER_FILE = PosixFilePermissions.asFileAttribute(setOf(OWNER_READ, OWNER_WRITE))
private const val STAGING = ".tmp-"

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

/** The CA a start works with and where it came from. */
class OpenedCa(
    val pair: CaKeyPair,
    val origin: CaOrigin,
)

/**
 * `<dir>/ca/{ca.crt,ca.key}`, owner-only. The `ca` directory appears by one atomic
 * rename, so concurrent first starts end up with the same CA: the loser loads the winner's.
 */
class CaDirectory(
    private val dir: Path,
    private val clock: Clock,
    private val writeFile: (Path, String) -> Unit = ::write,
) {
    private val ca = dir.resolve(CA)

    fun loadOrCreate(generate: () -> CaKeyPair): CaKeyPair = open(null, generate).pair

    /**
     * The CA of this directory. Empty directory: the CA of [source] when there is one (ADR 0052), else a
     * generated one. A CA that is there stays; a [source] must then name the same CA or the start is refused.
     */
    fun open(
        source: CaImport?,
        generate: () -> CaKeyPair,
    ): OpenedCa {
        writing(source) {
            if (Files.notExists(dir)) Files.createDirectories(dir, OWNER_DIR)
        }
        requireOwnerOnly(dir)
        writing(source, ::removeStaleStaging)
        if (Files.exists(ca)) return existing(source)
        val pair = source?.read()?.let { CaKeyPair(it.certificate, it.key) } ?: generate()
        val published = writing(source) { publish(pair) }
        return OpenedCa(load(), originOf(published, source))
    }

    private fun originOf(
        published: Boolean,
        source: CaImport?,
    ) = when {
        !published -> CaOrigin.EXISTING
        source == null -> CaOrigin.GENERATED
        else -> CaOrigin.IMPORTED
    }

    private fun existing(source: CaImport?): OpenedCa {
        val pair = load()
        source?.reconcile(CaFingerprint.of(pair.certificate))
        return OpenedCa(pair, CaOrigin.EXISTING)
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
        val staging = Files.createTempDirectory(dir, STAGING, OWNER_DIR)
        try {
            writeFile(staging.resolve(KEY), Pem.privateKey(pair.privateKey))
            writeFile(staging.resolve(CERT), Pem.certificate(pair.certificate))
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
