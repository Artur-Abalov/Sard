// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import jakarta.servlet.http.HttpServletRequest

/**
 * The client's address for rate limiting and logging (Р4): the TCP connection's own
 * remote address. X-Forwarded-For and X-Forwarded-Proto are not trusted; a reverse
 * proxy in front of Sard is a separate, later task.
 */
fun clientAddress(request: HttpServletRequest): String = request.remoteAddr
