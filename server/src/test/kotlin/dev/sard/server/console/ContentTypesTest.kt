// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.console

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals

@MutFlowTest
class ContentTypesTest {
    private fun typeOf(path: String): String = MutFlow.underTest { contentTypeOf(path) }

    @Test
    fun `the files of a Vite build have their own types`() {
        assertEquals("text/html;charset=UTF-8", typeOf("index.html"))
        assertEquals("text/javascript", typeOf("assets/index-Ab12Cd34.js"))
        assertEquals("text/css", typeOf("assets/index-Ab12Cd34.css"))
        assertEquals("font/woff2", typeOf("assets/plex-Ef56Gh78.woff2"))
        assertEquals("font/woff", typeOf("assets/plex-Ef56Gh78.woff"))
        assertEquals("image/svg+xml", typeOf("favicon.svg"))
    }

    @Test
    fun `any other file is an octet stream`() {
        assertEquals("application/octet-stream", typeOf("assets/data.bin"))
        assertEquals("application/octet-stream", typeOf("LICENSE"))
    }
}
