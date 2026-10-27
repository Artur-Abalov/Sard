// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** V6: a mismatch's raw fact name as the kind and subject the console and Telegram speak in words. */
@MutFlowTest
class MismatchWordingTest {
    private fun parse(name: String) = MutFlow.underTest { MismatchWording.of(name) }

    @Test
    fun `a fact name gets its kind and subject`() {
        val expected =
            mapOf(
                "rows:public.orders" to (MismatchKind.ROWS to "public.orders"),
                "tables" to (MismatchKind.TABLES to ""),
                "extension:postgis" to (MismatchKind.EXTENSION to "postgis"),
                "extension_version:hstore" to (MismatchKind.EXTENSION_VERSION to "hstore"),
                "control:/srv/data" to (MismatchKind.CONTROL_PATH to "/srv/data"),
                "files" to (MismatchKind.FILES to ""),
                "bytes" to (MismatchKind.BYTES to ""),
                "sha256:/srv/a.txt" to (MismatchKind.FILE_HASH to "/srv/a.txt"),
                "something_new" to (MismatchKind.OTHER to "something_new"),
            )

        for ((name, want) in expected) {
            val got = parse(name)
            assertEquals(want, got.kind to got.subject, name)
        }
    }

    @Test
    fun `a subject may itself contain colons`() {
        val got = parse("rows:public.we:ird")

        assertEquals("public.we:ird", got.subject)
    }

    @Test
    fun `a known prefix without a subject is another kind`() {
        assertEquals(MismatchKind.OTHER to "rows:", parse("rows:").let { it.kind to it.subject })
    }

    @Test
    fun `the kinds are named on the wire as the contract says`() {
        val wire = MismatchKind.entries.map { it.wire }

        assertEquals(
            listOf(
                "rows",
                "tables",
                "extension",
                "extension_version",
                "control_path",
                "files",
                "bytes",
                "file_hash",
                "other",
            ),
            wire,
        )
    }
}
