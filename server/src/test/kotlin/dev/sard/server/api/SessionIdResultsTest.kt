// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import kotlin.test.Test
import kotlin.test.assertTrue

/** A result that holds the id of a live session never prints it: a log line or an assertion message would leak it. */
class SessionIdResultsTest {
    @Test
    fun `the setup code result does not print the session id`() {
        assertTrue("x-id" !in CodeResult.Accepted("x-id").toString())
    }

    @Test
    fun `the admin step result does not print the session id`() {
        assertTrue("x-id" !in AdminStepResult.Done("x-id").toString())
    }

    @Test
    fun `the password change result does not print the session id`() {
        assertTrue("x-id" !in PasswordChangeResult.Changed("x-id").toString())
    }

    @Test
    fun `the sign-in result does not print the session id`() {
        assertTrue("x-id" !in SignInResult.SignedIn("x-id").toString())
    }
}
