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
                "rows:public.orders" to ("rows" to "public.orders"),
                "tables" to ("tables" to ""),
                "extension:postgis" to ("extension" to "postgis"),
                "extension_version:hstore" to ("extension_version" to "hstore"),
                "control:/srv/data" to ("control_path" to "/srv/data"),
                "files" to ("files" to ""),
                "bytes" to ("bytes" to ""),
                "sha256:/srv/a.txt" to ("file_hash" to "/srv/a.txt"),
                "something_new" to ("other" to "something_new"),
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
        assertEquals("other" to "rows:", parse("rows:").let { it.kind to it.subject })
    }
}
