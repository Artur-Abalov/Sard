// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.pki.CaFingerprint
import dev.sard.server.pki.FakeCaLedger
import dev.sard.server.pki.FileCertificateAuthority
import dev.sard.server.pki.PkiFixtures
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.io.TempDir
import java.nio.file.Path
import kotlin.test.Test
import kotlin.test.assertEquals

@MutFlowTest
class CaInfoControllerTest {
    @TempDir
    lateinit var tmp: Path

    @Test
    fun `the CA information carries the fingerprint the tokens carry`() {
        val names = PkiFixtures.SERVER_NAMES
        val ca = FileCertificateAuthority(tmp.resolve("pki"), names, PkiFixtures.CLOCK, PkiFixtures.random(), FakeCaLedger())
        val info = MutFlow.underTest { CaInfoController(ca).info() }
        val keyPath = tmp.resolve("pki/ca/ca.key").toAbsolutePath().toString()
        assertEquals(CaInfo(ca.fingerprint().hex, CaOrigin.GENERATED, keyPath), info)
        assertEquals(64, info.fingerprint.length)
        assertEquals(CaFingerprint.of(PkiFixtures.certificate(ca.caBundlePem())).hex, info.fingerprint)
    }
}
