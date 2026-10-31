// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import jakarta.servlet.http.HttpServletRequest
import org.springframework.http.ResponseCookie
import org.springframework.security.core.context.SecurityContextHolder

/**
 * Name of the request attribute the core's own chain attaches the touched [dev.sard.server.auth.AdminSession] under,
 * for the code of the core that needs more than the subject (the tenant of the session). An extension is not asked
 * to set it: it authenticates through the security context (CORE1 Р43).
 */
const val SESSION_REQUEST_ATTRIBUTE = "dev.sard.server.session"

/** The value of the request's cookie [name], or null when it carries none. */
fun HttpServletRequest.cookie(name: String): String? = cookies.orEmpty().firstOrNull { it.name == name }?.value

/**
 * True when the chain authenticated this request (Р43): the core's session cookie, or the sign-in of an extension.
 * The first-start wizard learns from it that the caller is signed in.
 */
fun HttpServletRequest.administratorSession(): Boolean {
    val authentication = SecurityContextHolder.getContext().authentication
    return authentication?.isAuthenticated == true
}

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

private fun setupCookieBuilder(
    id: String,
    secure: Boolean,
) = ResponseCookie
    .from(SETUP_COOKIE, id)
    .httpOnly(true)
    .sameSite("Strict")
    .path(SETUP_COOKIE_PATH)
    .secure(secure)

/** A fresh setup session cookie (F4a, Р3): the path of the wizard only, no Max-Age, no Expires. */
fun setupCookie(
    id: String,
    secure: Boolean,
): ResponseCookie = setupCookieBuilder(id, secure).build()

/** A cookie that erases [SETUP_COOKIE] in the browser when the admin step is done. */
fun clearedSetupCookie(secure: Boolean): ResponseCookie = setupCookieBuilder("", secure).maxAge(0).build()
