// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

/**
 * Operations whose security requirement is empty: sign-in itself, the public status endpoint, and the two the
 * first-start wizard opens without a session (F4a, К1, К2). The contract test compares this set with OpenAPI.
 */
internal val PUBLIC_OPERATIONS =
    setOf(
        "POST /api/v1/session",
        "GET /api/v1/status",
        "GET /api/v1/onboarding",
        "POST /api/v1/onboarding/setup-session",
    )

/**
 * Operations for which the chain does not even look at a session: sign-in itself, the status and the entering of the
 * setup code (Е11, Р52). The session they carry is neither read nor extended.
 */
internal val SESSIONLESS_OPERATIONS =
    setOf("POST /api/v1/session", "GET /api/v1/status", "POST /api/v1/onboarding/setup-session")

/**
 * The state of the wizard (К1) is public and tells which session came with the request; the steps (К3, К4) ask for
 * a setup session, which the controller checks because the chain does not know setup sessions. All three pass
 * the chain with or without an administrator session; a valid one authenticates the request (Р35).
 */
internal val WIZARD_OPERATIONS =
    setOf("GET /api/v1/onboarding", "POST /api/v1/onboarding/ca", "POST /api/v1/onboarding/admin")

/** Sign-out needs a session but is not subject to the access policy (Р17, Р40). */
internal const val SIGN_OUT = "DELETE /api/v1/session"

private const val API_ROOT = "/api/v1"

/** True for /api/v1 and every path under it: the part of the server the chain guards (Р35, Р37, Р47). */
internal fun isApiPath(requestUri: String): Boolean = requestUri == API_ROOT || requestUri.startsWith("$API_ROOT/")
