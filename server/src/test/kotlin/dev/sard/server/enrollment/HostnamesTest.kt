// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** Rule "hostname — от 1 до 253 символов, без управляющих символов" (Enroll decision 8, Register S4a). */
@MutFlowTest
class HostnamesTest {
    private fun valid(hostname: String): Boolean = MutFlow.underTest { Hostnames.isValid(hostname) }

    @Test
    fun `1 to 253 characters are valid, whatever the script`() {
        for (hostname in listOf("d", "db1", "db1.example.com", "a".repeat(253), "сервер-1")) {
            assertEquals(true, valid(hostname), hostname)
        }
    }

    @Test
    fun `empty or longer than 253 characters is invalid`() {
        assertEquals(false, valid(""))
        assertEquals(false, valid("a".repeat(254)))
    }

    @Test
    fun `any control character is invalid, NUL included`() {
        for (control in listOf('\u0000', '\n', '\u001f', '\u007f', '\u0085', '\u009f')) {
            assertEquals(false, valid("db${control}1"), "U+%04X".format(control.code))
        }
    }

    @Test
    fun `the characters just outside the control ranges are valid`() {
        for (neighbour in listOf(' ', '~', ' ')) {
            assertEquals(true, valid("db${neighbour}1"), "U+%04X".format(neighbour.code))
        }
    }
}
