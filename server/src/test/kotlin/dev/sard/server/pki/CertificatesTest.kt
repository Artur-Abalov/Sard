// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.NOW
import dev.sard.server.pki.PkiFixtures.random
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Duration
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private const val KEY_CERT_SIGN = 5
private const val CRL_SIGN = 6

@MutFlowTest
class CertificatesTest {
    @Test
    fun `the root is a self-signed ten-year CA able to sign an intermediate`() {
        val keys = Keys.generate(random())
        val root = MutFlow.underTest { Certificates.root(keys, NOW, random()) }
        root.verify(keys.public)
        assertEquals("CN=Sard CA", root.subjectX500Principal.name)
        assertEquals(Int.MAX_VALUE, root.basicConstraints, "CA without a path length limit")
        assertEquals(listOf(KEY_CERT_SIGN, CRL_SIGN), root.keyUsage.indices.filter { root.keyUsage[it] })
        assertEquals(NOW - Duration.ofHours(1), root.notBefore.toInstant())
        assertEquals(NOW + Duration.ofDays(3650), root.notAfter.toInstant())
        assertEquals(128, root.serialNumber.bitLength())
        assertTrue(root.subjectX500Principal == root.issuerX500Principal)
    }
}
