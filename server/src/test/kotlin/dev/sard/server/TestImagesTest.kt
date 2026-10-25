// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

class TestImagesTest {
    @Test
    fun `a pinned tag gives its reference by digest`() {
        val ref = TestImages.pinned("postgres:16")
        assertTrue(ref.matches(Regex("postgres:16@sha256:[0-9a-f]{64}")), ref)
    }

    @Test
    fun `a tag is matched whole, not as a prefix`() {
        assertFailsWith<IllegalStateException> { TestImages.pinned("postgres:1") }
    }

    @Test
    fun `an unpinned tag fails instead of pulling it`() {
        val e = assertFailsWith<IllegalStateException> { TestImages.pinned("postgres:9") }
        assertEquals("postgres:9: not in test/images.env", e.message)
    }
}
