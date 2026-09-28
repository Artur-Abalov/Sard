// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import org.springframework.http.ResponseCookie

/** A fresh session cookie (Р7: no Max-Age, no Expires). */
fun sessionCookie(
    id: String,
    secure: Boolean,
): ResponseCookie =
    ResponseCookie
        .from(SESSION_COOKIE, id)
        .httpOnly(true)
        .sameSite("Strict")
        .path("/")
        .secure(secure)
        .build()

/** A cookie that erases [SESSION_COOKIE] in the browser (Р12, logout). */
fun clearedSessionCookie(secure: Boolean): ResponseCookie =
    ResponseCookie
        .from(SESSION_COOKIE, "")
        .httpOnly(true)
        .sameSite("Strict")
        .path("/")
        .secure(secure)
        .maxAge(0)
        .build()
