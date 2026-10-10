// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import jakarta.servlet.http.HttpServletRequest
import org.springframework.http.ResponseCookie

/**
 * Name of the request attribute an authentication filter attaches the touched administrator session under.
 * The open core's [dev.sard.server.api.OnboardingApi] reads only its presence; an enterprise filter that
 * replaces the core one sets it the same way, and that is how the wizard learns the caller is signed in.
 */
const val SESSION_REQUEST_ATTRIBUTE = "dev.sard.server.session"

/** The value of the request's cookie [name], or null when it carries none. */
fun HttpServletRequest.cookie(name: String): String? = cookies.orEmpty().firstOrNull { it.name == name }?.value

/** True when an authentication filter attached an administrator session to this request. */
fun HttpServletRequest.administratorSession(): Boolean = getAttribute(SESSION_REQUEST_ATTRIBUTE) != null

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
