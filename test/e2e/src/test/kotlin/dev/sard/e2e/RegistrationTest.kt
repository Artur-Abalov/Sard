// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import io.grpc.Status
import org.junit.jupiter.api.extension.RegisterExtension
import java.util.Base64
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * The agent authentication interceptor (S3) from outside: AgentService refuses a caller without
 * a client certificate. That a certificate issued by `sard-agent enroll` passes is a scenario of
 * the enroll spec ([AgentEnrollTest]); a token used twice is refused there too (TOKEN_USED).
 */
class RegistrationTest {
    @Test
    fun `without a client certificate AgentService is UNAUTHENTICATED`() {
        val root = ServerTls.presentedChain(sard).last()
        val caPem = "-----BEGIN CERTIFICATE-----\n" + Base64.getMimeEncoder().encodeToString(root.encoded) + "\n-----END CERTIFICATE-----\n"
        assertEquals(Status.Code.UNAUTHENTICATED, ServerTls.renewCertificateStatus(sard, ServerTls.trusting(caPem).build()))
    }

    companion object {
        @JvmField
        @RegisterExtension
        val sard = SardEnvironment()
    }
}
