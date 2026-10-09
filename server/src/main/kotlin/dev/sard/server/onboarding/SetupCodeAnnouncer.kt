// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.auth.AdminSetup
import org.slf4j.LoggerFactory
import org.springframework.context.SmartLifecycle
import java.time.temporal.ChronoUnit

private val log = LoggerFactory.getLogger(SetupCodeAnnouncer::class.java)

/**
 * At every start while there is no administrator: issues the setup code and prints it, once, in the one line
 * the operator's scripts look for (Р1). The code is in no other line, response or metric. A restart
 * before the admin step gives a new code, the old one is gone with the process.
 */
class SetupCodeAnnouncer(
    private val adminSetup: AdminSetup,
    private val codes: SetupCodes,
) : SmartLifecycle {
    @Volatile
    private var running = false

    override fun start() {
        if (!adminSetup.done()) announce()
        running = true
    }

    private fun announce() {
        val issued = codes.issue()
        val until = issued.expiresAt.truncatedTo(ChronoUnit.SECONDS)
        log.info("SARD SETUP: the server has no administrator. Open /setup in the console and enter the code below.")
        log.info("SARD SETUP CODE: {} valid until {}", issued.display, until)
        log.info("SARD SETUP: to read the code again, run: docker compose logs server")
    }

    override fun stop() {
        running = false
    }

    override fun isRunning() = running
}
