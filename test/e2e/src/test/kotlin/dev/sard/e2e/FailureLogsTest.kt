// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.e2e

import java.nio.file.Files
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertFails
import kotlin.test.assertFalse

/** The harness itself: a failing start leaves the container logs, secrets masked. */
class FailureLogsTest {
    @Test
    fun `a server that refuses its configuration fails the start and leaves masked logs`() {
        // The endpoint's host is not among the certificate names: the server refuses to start.
        val env = SardEnvironment(serverEnv = mapOf("SARD_AGENT_ENDPOINT" to "$PLANTED:9090"))
        // Stands for a token or key a test registered: the server's error quotes it.
        env.secret(PLANTED)
        try {
            assertFails { env.start(FailureLogsTest::class.simpleName!!) }
            val log = Files.readString(E2e.logsDir.resolve("FailureLogsTest/start/sard-server.log"))
            assertContains(log, "is not covered by any of sard.pki.server-names")
            assertContains(log, "[redacted]")
            assertFalse(PLANTED in log, "a registered secret reached the log")
        } finally {
            env.stop()
        }
    }

    @Test
    fun `private keys and registered secrets are masked`() {
        val key = "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49\n-----END PRIVATE KEY-----"
        assertContains(Redaction.apply("key:\n$key\nend", emptySet()), "key:\n[redacted private key]\nend")
        assertContains(Redaction.apply("password=hunter2hunter2", setOf("hunter2hunter2")), "password=[redacted]")
    }

    private companion object {
        const val PLANTED = "planted-secret.example"
    }
}
