// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

class SessionCookiesTest {
    @Test
    fun `a fresh session cookie is HttpOnly, SameSite=Strict, Path root, and sets no Max-Age or Expires`() {
        val cookie = sessionCookie("abc123", secure = false)
        assertEquals("sard_session", cookie.name)
        assertEquals("abc123", cookie.value)
        assertTrue(cookie.isHttpOnly)
        assertEquals("Strict", cookie.sameSite)
        assertEquals("/", cookie.path)
        assertFalse(cookie.isSecure)
        val rendered = cookie.toString()
        assertFalse(rendered.contains("Max-Age"))
        assertFalse(rendered.contains("Expires"))
    }

    @Test
    fun `a session cookie over HTTPS is marked Secure`() {
        assertTrue(sessionCookie("abc123", secure = true).isSecure)
    }

    @Test
    fun `a cleared session cookie has an empty value and Max-Age 0`() {
        val cookie = clearedSessionCookie(secure = false)
        assertEquals("", cookie.value)
        assertEquals(0, cookie.maxAge.seconds)
        assertEquals("/", cookie.path)
    }
}
