// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import kotlin.test.Test
import kotlin.test.assertEquals

/** The name an enterprise authentication filter must set for an administrator (ADR 0021, the seam; F4a Р16). */
class SessionRequestAttributeTest {
    @Test
    fun `the request attribute of an administrator session is the one the enterprise contract names`() {
        assertEquals("dev.sard.server.session", SESSION_REQUEST_ATTRIBUTE)
    }
}
