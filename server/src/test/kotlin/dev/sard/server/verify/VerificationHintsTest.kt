// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.verify

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

/** V6 (rule 9): what a source's config lacks to be verified well, as a pure function of plugin and config. */
@MutFlowTest
class VerificationHintsTest {
    private fun hints(
        plugin: String,
        config: String,
    ) = MutFlow.underTest { VerificationHints.of(plugin, config) }

    @Test
    fun `PostgreSQL without key tables is asked for them`() {
        assertEquals(listOf(VerificationHint.KEY_TABLES_MISSING), hints("postgresql", """{"database":"app"}"""))
    }

    @Test
    fun `an empty list of key tables is no key tables`() {
        assertEquals(listOf(VerificationHint.KEY_TABLES_MISSING), hints("postgresql", """{"key_tables":[]}"""))
    }

    @Test
    fun `PostgreSQL with key tables needs no hint`() {
        assertEquals(emptyList(), hints("postgresql", """{"key_tables":["public.orders"]}"""))
    }

    @Test
    fun `files without control paths are asked for them`() {
        assertEquals(listOf(VerificationHint.CONTROL_PATHS_MISSING), hints("files", """{"paths":["/srv"]}"""))
        assertEquals(listOf(VerificationHint.CONTROL_PATHS_MISSING), hints("files", """{"control_paths":[]}"""))
    }

    @Test
    fun `files with control paths need no hint`() {
        assertEquals(emptyList(), hints("files", """{"control_paths":["/srv/www"]}"""))
    }

    @Test
    fun `another plugin gets no hint`() {
        assertEquals(emptyList(), hints("mysql", "{}"))
    }
}
