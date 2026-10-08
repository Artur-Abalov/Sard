// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import org.bouncycastle.asn1.x509.SubjectPublicKeyInfo
import org.slf4j.LoggerFactory
import java.nio.file.Path
import java.security.SecureRandom
import java.time.Clock
import java.time.Duration
import java.util.concurrent.atomic.AtomicLong
import javax.net.ssl.X509KeyManager

private val log = LoggerFactory.getLogger(FileCertificateAuthority::class.java)

/** The server certificate lives 90 days and is replaced in its last 30. */
private val RENEW_BEFORE = Duration.ofDays(30)

/**
 * The open core's CA: root key in a file under [dir] (see [CaDirectory]), server key in
 * memory only, re-issued on every start. Losing [dir] means enrolling every agent again.
 */
class FileCertificateAuthority(
    dir: Path,
    private val serverNames: List<String>,
    private val clock: Clock,
    private val random: SecureRandom,
    importDir: Path? = null,
) : CertificateAuthority {
    init {
        ServerNames.validate(serverNames)
    }

    private val opened =
        CaDirectory(dir, clock).open(importDir?.let { CaImportSource(it, clock) }) { CaKeyPair.generate(clock, random) }
    private val ca = opened.pair
    private val bundle = Pem.certificate(ca.certificate)
    private val fingerprint = CaFingerprint.of(ca.certificate).also { logStart(it, opened.origin, importDir) }
    private val generation = AtomicLong()
    private val serverKeys = ServerKeyManager(issueServerKey())

    override fun caBundlePem() = bundle

    override fun fingerprint() = fingerprint

    override fun serverKeyManager(): X509KeyManager = serverKeys

    override fun renewServerCertificate(): Boolean {
        val due = clock.instant() >= serverKeys.certificate().notAfter.toInstant() - RENEW_BEFORE
        if (due) serverKeys.replace(issueServerKey())
        return due
    }

    override fun issueAgentCertificate(
        csrDer: ByteArray,
        agent: AgentIdentity,
    ): IssuedCertificate {
        val certificate = Certificates.agent(AgentCsr.verifiedKey(csrDer), agent, ca, clock.instant(), random)
        val notAfter = certificate.notAfter.toInstant()
        return IssuedCertificate(Pem.certificate(certificate), certificate.serialNumber, notAfter)
    }

    private fun issueServerKey(): ServerKey {
        val keys = Keys.generate(random)
        val spki = SubjectPublicKeyInfo.getInstance(keys.public.encoded)
        val certificate = Certificates.server(spki, serverNames, ca, clock.instant(), random)
        // The root travels with the leaf: a new agent pins it by the fingerprint in its token (S2a).
        val chain = arrayOf(certificate, ca.certificate)
        return ServerKey("sard-server-${generation.incrementAndGet()}", keys.private, chain)
    }

    /** Never prints key material. */
    override fun toString() = "FileCertificateAuthority(${fingerprint.hex})"

    /** The fingerprint with the origin of the CA, at every start, so a restored server can be compared with the old one. */
    private fun logStart(
        fingerprint: CaFingerprint,
        origin: CaOrigin,
        importDir: Path?,
    ) {
        val name = origin.name.lowercase()
        if (origin == CaOrigin.IMPORTED) {
            log.info("CA imported from {}: fingerprint={} origin={}", importDir, fingerprint.hex, name)
        } else {
            log.info("CA fingerprint={} origin={}", fingerprint.hex, name)
        }
    }
}
