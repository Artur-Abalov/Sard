// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.time.Instant
import kotlin.test.Test
import kotlin.test.assertEquals

private val AT: Instant = Instant.parse("2026-10-10T03:00:00Z")

/** ВК32: the destination a restore form starts from, `<source name>/<snapshot time>`, safe as a path. */
@MutFlowTest
class RestoreDefaultsTest {
    private fun destination(name: String) = MutFlow.underTest { RestoreDefaults.destination(name, AT) }

    @Test
    fun `letters, digits, underscore and dash of a name stay, the time is UTC`() {
        assertEquals("prod-DB_01/20261010T030000Z", destination("prod-DB_01"))
        val newYear = Instant.parse("2026-01-01T00:00:00Z")
        assertEquals("a/20260101T000000Z", MutFlow.underTest { RestoreDefaults.destination("a", newYear) })
    }

    @Test
    fun `every other byte of a name is escaped, so a name never makes a path of its own`() {
        assertEquals("etc%20data/20261010T030000Z", destination("etc data"))
        assertEquals("a%2Fb/20261010T030000Z", destination("a/b"))
        assertEquals("%2E%2E/20261010T030000Z", destination(".."))
        assertEquals("%C3%A9t%C3%A9/20261010T030000Z", destination("été"))
        assertEquals("%00%7F/20261010T030000Z", destination("\u0000\u007f"))
        assertEquals("%25%2F%5C/20261010T030000Z", destination("%/\\"))
    }
}
