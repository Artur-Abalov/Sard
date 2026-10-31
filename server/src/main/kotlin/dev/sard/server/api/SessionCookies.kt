// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.SubjectSource
import jakarta.servlet.http.HttpServletRequest
import org.springframework.http.ResponseCookie
import org.springframework.security.authentication.AnonymousAuthenticationToken
import org.springframework.security.core.Authentication
import org.springframework.security.core.context.SecurityContextHolder

/**
 * Name of the request attribute the core's own chain attaches the touched [dev.sard.server.auth.AdminSession] under.
 * It is the hook of a tenant resolver (ADR 0013, OQ-035) that wants the session of the request; no main code of the
 * core reads it. An extension is not asked to set it: it authenticates through the security context (CORE1 Р43).
 */
const val SESSION_REQUEST_ATTRIBUTE = "dev.sard.server.session"

/** The value of the request's cookie [name], or null when it carries none. */
fun HttpServletRequest.cookie(name: String): String? = cookies.orEmpty().firstOrNull { it.name == name }?.value

/** Signed in: an authentication that is authenticated and not the anonymous one (Р38, Р43). */
fun Authentication?.isSignedIn(): Boolean = this != null && isAuthenticated && this !is AnonymousAuthenticationToken

/**
 * True when the request is signed in (the core's session cookie, or the sign-in of an extension) and the
 * [SubjectSource] names a subject for it (Р43). The first-start wizard learns from it that the caller is an
 * administrator; a failing source says no.
 */
fun administratorSession(subjects: SubjectSource): Boolean {
    val named = runCatching { subjects.current() }.getOrNull()
    return SecurityContextHolder.getContext().authentication.isSignedIn() && named != null
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
