// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.io.ByteArrayInputStream
import java.security.SecureRandom
import java.security.cert.CertificateFactory
import java.security.cert.X509Certificate
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset
import java.util.UUID

/** Test fixtures: Go-made CSRs (resources/pki/gencsr.go) and a fixed clock. */
object PkiFixtures {
    val NOW: Instant = Instant.parse("2026-09-27T10:00:00Z")
    val CLOCK: Clock = Clock.fixed(NOW, ZoneOffset.UTC)
    val AGENT =
        AgentIdentity(
            tenantId = UUID.fromString("7f3c1a52-0b4e-4c1d-9a55-2d8e6f0b9c11"),
            agentId = UUID.fromString("0c9d2b4e-5a61-4f7e-8b3a-1d2e3f4a5b6c"),
        )
    val SERVER_NAMES = listOf("localhost", "127.0.0.1", "::1")

    fun resource(name: String): ByteArray =
        checkNotNull(PkiFixtures::class.java.getResourceAsStream("/pki/$name")) { "missing fixture $name" }
            .readAllBytes()

    fun certificate(pem: String): X509Certificate =
        CertificateFactory
            .getInstance("X.509")
            .generateCertificate(ByteArrayInputStream(pem.toByteArray())) as X509Certificate

    fun certificates(pem: String): List<X509Certificate> =
        CertificateFactory
            .getInstance("X.509")
            .generateCertificates(ByteArrayInputStream(pem.toByteArray()))
            .map { it as X509Certificate }

    fun random() = SecureRandom()
}
