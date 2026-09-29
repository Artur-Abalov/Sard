// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * Rule "Установка показывает, где задать пароль администратора" (@qa-only in the spec,
 * but the shape of `deploy/.env.example` itself — an empty placeholder, not a value
 * someone might copy verbatim into deploy/.env and forget to change — is a plain fact
 * about a committed file, so it gets a real test).
 */
class DeployEnvExampleTest {
    private val lines: List<String>
        get() {
            val file = File("../deploy/.env.example")
            check(file.isFile) { "expected to run with the server module as the working directory: $file" }
            return file.readLines()
        }

    @Test
    fun `SARD_ADMIN_PASSWORD is an empty placeholder, not a value to copy verbatim`() {
        val value = lines.first { it.startsWith("SARD_ADMIN_PASSWORD=") }.substringAfter("=")
        assertEquals("", value)
    }

    @Test
    fun `SARD_ADMIN_PASSWORD is commented with the minimum length, next to SARD_AGENT_ENDPOINT`() {
        val text = lines.joinToString("\n")
        assertTrue(text.contains("SARD_ADMIN_PASSWORD"), text)
        assertTrue(text.contains("12"), text)
        assertTrue(text.contains("SARD_AGENT_ENDPOINT"), text)
    }
}
