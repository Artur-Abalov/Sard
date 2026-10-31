// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import kotlin.test.Test
import kotlin.test.assertEquals

/** The request attribute the chain puts the session under: the hook of a tenant resolver (ADR 0071). */
class SessionRequestAttributeTest {
    @Test
    fun `the request attribute of an administrator session is the one the enterprise contract names`() {
        assertEquals("dev.sard.server.session", SESSION_REQUEST_ATTRIBUTE)
    }
}
