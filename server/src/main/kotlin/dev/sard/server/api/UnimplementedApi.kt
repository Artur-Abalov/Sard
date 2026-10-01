// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import org.springframework.http.HttpStatus
import org.springframework.http.ProblemDetail
import org.springframework.web.ErrorResponseException

/** Body of a stub whose behavior S8b implements. */
fun notImplemented(): Nothing {
    val problem = ProblemDetail.forStatus(HttpStatus.NOT_IMPLEMENTED)
    problem.setProperty("code", "not_implemented")
    throw ErrorResponseException(HttpStatus.NOT_IMPLEMENTED, problem, null)
}
