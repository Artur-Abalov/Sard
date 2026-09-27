// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.pki

import java.time.Duration
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import javax.net.ssl.X509KeyManager
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

class ServerCertificateRenewalTest {
    /** Counts renewal requests; answers "renewed" on every other one. */
    private class CountingCa(
        val calls: CountDownLatch,
    ) : CertificateAuthority {
        override fun caBundlePem() = error("unused")

        override fun fingerprint() = error("unused")

        override fun serverKeyManager(): X509KeyManager = error("unused")

        override fun issueAgentCertificate(
            csrDer: ByteArray,
            agent: AgentIdentity,
        ) = error("unused")

        override fun renewServerCertificate(): Boolean {
            calls.countDown()
            return calls.count % 2 == 0L
        }
    }

    @Test
    fun `the renewal asks the CA periodically while running and stops with the context`() {
        val calls = CountDownLatch(3)
        val renewal = ServerCertificateRenewal(CountingCa(calls), Duration.ofMillis(10))
        renewal.start()
        assertTrue(renewal.isRunning)
        assertTrue(calls.await(5, TimeUnit.SECONDS), "renewal was not requested three times")
        renewal.stop()
        assertEquals(false, renewal.isRunning)
    }
}
