// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * The shape of `deploy/.env.example`: an empty placeholder, not a value someone might copy verbatim into
 * deploy/.env and forget to change. The administrator password is no setting any more (F4a): the first start
 * asks for it in the wizard, and the scenario that the example does not mention it is the task of the
 * installation (docs/specs/server/onboarding-setup.feature, "Compose и пример окружения не содержат пароль").
 */
class DeployEnvExampleTest {
    private val lines: List<String>
        get() {
            val file = File("../deploy/.env.example")
            check(file.isFile) { "expected to run with the server module as the working directory: $file" }
            return file.readLines()
        }

    @Test
    fun `SARD_TELEGRAM_BOT_TOKEN is an empty placeholder, and the chat id goes with it`() {
        val value = lines.first { it.startsWith("SARD_TELEGRAM_BOT_TOKEN=") }.substringAfter("=")
        assertEquals("", value)
        assertTrue(lines.any { it.contains("SARD_TELEGRAM_CHAT_ID=") }, lines.joinToString("\n"))
    }
}
