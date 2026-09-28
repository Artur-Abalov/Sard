// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.api.ErrorCode
import dev.sard.server.api.PROBLEM_JSON
import dev.sard.server.api.Problem
import jakarta.servlet.http.HttpServletResponse
import tools.jackson.databind.ObjectMapper

/** Writes an RFC 9457 problem body directly to a servlet response (filters run before Spring MVC). */
fun writeProblem(
    response: HttpServletResponse,
    objectMapper: ObjectMapper,
    status: Int,
    title: String,
    code: ErrorCode,
) {
    val problem = Problem(type = "about:blank", title = title, status = status, detail = null, code = code)
    response.status = status
    // Set directly (not response.characterEncoding), so Tomcat does not append ";charset=..." to it:
    // the contract's Content-Type is exactly "application/problem+json".
    response.setHeader("Content-Type", PROBLEM_JSON)
    response.outputStream.write(objectMapper.writeValueAsBytes(problem))
}
