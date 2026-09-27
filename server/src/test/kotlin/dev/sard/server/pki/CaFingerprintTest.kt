// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import dev.sard.server.pki.PkiFixtures.certificate
import dev.sard.server.pki.PkiFixtures.resource
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

@MutFlowTest
class CaFingerprintTest {
    /**
     * golden-ca.crt was made by openssl; the expected value is computed outside Sard:
     * openssl x509 -in golden-ca.crt -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum
     */
    @Test
    fun `the fingerprint is SHA-256 of the SubjectPublicKeyInfo, as openssl computes it`() {
        val golden = certificate(String(resource("golden-ca.crt")))
        val fingerprint = MutFlow.underTest { CaFingerprint.of(golden) }
        assertEquals("b6d8d204caa586104e9b5e7e9fc8da43dbc8fc8715b6fd8059120ebf5691e61f", fingerprint.hex)
    }
}
