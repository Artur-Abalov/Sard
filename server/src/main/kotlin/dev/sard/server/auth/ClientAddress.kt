// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import jakarta.servlet.http.HttpServletRequest

/**
 * The client's address for rate limiting and logging (Р4): the TCP connection's own
 * remote address. With SARD_FORWARD_HEADERS=native Tomcat has already replaced it with
 * the address from X-Forwarded-For of a trusted proxy (ADR 0045).
 */
fun clientAddress(request: HttpServletRequest): String = request.remoteAddr
