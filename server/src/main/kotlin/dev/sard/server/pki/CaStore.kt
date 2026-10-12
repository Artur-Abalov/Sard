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
import java.nio.file.Path
import java.nio.file.StandardCopyOption.ATOMIC_MOVE
import java.nio.file.StandardOpenOption.CREATE_NEW
import java.nio.file.StandardOpenOption.READ
import java.nio.file.StandardOpenOption.WRITE
import java.nio.file.attribute.PosixFilePermission.OWNER_READ
import java.nio.file.attribute.PosixFilePermission.OWNER_WRITE
import java.nio.file.attribute.PosixFilePermissions
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.time.Clock
import java.util.UUID

internal val OWNER_DIR = PosixFilePermissions.asFileAttribute(OWNER)
private val OWNER_FILE = PosixFilePermissions.asFileAttribute(setOf(OWNER_READ, OWNER_WRITE))
internal const val STAGING = ".tmp-"
internal const val REPLACED = ".tmp-replaced-"

/**
 * The files of `<dir>/ca/{ca.crt,ca.key}`: reading them, publishing a CA by one rename, swapping it for another
 * and cleaning up after a start that died half way. Nothing here knows the database or who may replace a CA.
 */
internal class CaStore(
    private val dir: Path,
    private val clock: Clock,
    private val writeFile: (Path, String) -> Unit,
) {
    private val ca = dir.resolve(CA)
    private val leftovers = CaLeftovers(dir, clock)

    /** The `ca` directory, for messages. */
    val caPath: Path get() = ca

    fun hasCa(): Boolean = Files.exists(ca)

    fun createDirectory() {
        if (Files.notExists(dir)) Files.createDirectories(dir, OWNER_DIR)
    }

    fun requireDirectoryOwnerOnly() = requireOwnerOnly(dir)

    /** Puts back the CA of a replacement that died between its renames, and removes what start-ups left behind. */
    fun tidy() = leftovers.tidy()

    /** True when this call published [pair], false when another start did first. */
    fun publish(pair: CaKeyPair): Boolean {
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

    /**
     * Swaps the CA in place for [pair] (Р11): the new one is written whole in a staging directory, the present one
     * is renamed away, the new one renamed in, the old one deleted. A start that dies between the renames finds
     * the old CA under its new name and puts it back ([tidy]).
     */
    fun replace(pair: CaKeyPair) {
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

    fun load(): CaKeyPair {
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

    /** Makes the rename durable; not observable without a crash, so no test covers it. */
    private fun syncDirectory() { // mutflow:falsePositive fsync changes nothing observable without an OS crash
        val channel = FileChannel.open(dir, READ)
        try {
            channel.force(true)
        } finally {
            channel.close()
        }
    }

    private fun requireOwnerOnly(path: Path) {
        ownerOnlyViolation(path)?.let { throw InsecureKeyStorageException(it) }
    }
}

internal fun write(
    path: Path,
    text: String,
) {
    FileChannel.open(path, setOf(CREATE_NEW, WRITE), OWNER_FILE).use {
        it.write(ByteBuffer.wrap(text.toByteArray()))
        it.force(true)
    }
}
