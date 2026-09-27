// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import org.bouncycastle.asn1.pkcs.PrivateKeyInfo
import org.bouncycastle.openssl.jcajce.JcaPEMKeyConverter
import java.nio.ByteBuffer
import java.nio.channels.FileChannel
import java.nio.file.FileSystemException
import java.nio.file.Files
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

private const val CA = "ca"
private const val CERT = "ca.crt"
private const val KEY = "ca.key"
private val OWNER = setOf(OWNER_READ, OWNER_WRITE, OWNER_EXECUTE)
private val OWNER_DIR = PosixFilePermissions.asFileAttribute(OWNER)
private val OWNER_FILE = PosixFilePermissions.asFileAttribute(setOf(OWNER_READ, OWNER_WRITE))

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

/**
 * `<dir>/ca/{ca.crt,ca.key}`, owner-only. The `ca` directory appears by one atomic
 * rename, so concurrent first starts end up with the same CA: the loser loads the winner's.
 */
class CaDirectory(
    private val dir: Path,
) {
    private val ca = dir.resolve(CA)

    fun loadOrCreate(generate: () -> CaKeyPair): CaKeyPair {
        if (Files.notExists(dir)) Files.createDirectories(dir, OWNER_DIR)
        requireOwnerOnly(dir)
        if (Files.notExists(ca)) publish(generate())
        return load()
    }

    private fun publish(pair: CaKeyPair) {
        val staging = Files.createTempDirectory(dir, ".tmp-", OWNER_DIR)
        try {
            write(staging.resolve(KEY), Pem.privateKey(pair.privateKey))
            write(staging.resolve(CERT), Pem.certificate(pair.certificate))
            Files.move(staging, ca, ATOMIC_MOVE)
            syncDirectory()
        } catch (e: FileSystemException) {
            if (Files.notExists(ca)) throw e
        } finally {
            staging.toFile().deleteRecursively()
        }
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

    private fun write(
        path: Path,
        text: String,
    ) {
        FileChannel.open(path, setOf(CREATE_NEW, WRITE), OWNER_FILE).use {
            it.write(ByteBuffer.wrap(text.toByteArray()))
            it.force(true)
        }
    }
}
