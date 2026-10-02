// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.http.ResponseEntity

private val PROBLEM = MediaType.parseMediaType(PROBLEM_JSON)
private const val ABOUT_BLANK = "about:blank"

// Every body of an error is one of the contract's problem classes (S8b, "Ошибки — problem+json с кодом
// контракта"), so the OpenAPI document and the wire agree. What a request carried (a config, a token)
// never goes into a title or a detail.

internal fun problemResponse(
    status: HttpStatus,
    title: String,
    code: ErrorCode,
): ResponseEntity<Problem> =
    ResponseEntity.status(status).contentType(PROBLEM).body(Problem(ABOUT_BLANK, title, status.value(), null, code))

internal fun notFoundResponse() = problemResponse(HttpStatus.NOT_FOUND, "Not Found", ErrorCode.NOT_FOUND)

internal fun unprocessableResponse(
    code: ErrorCode,
    errors: List<FieldError>,
): ResponseEntity<ValidationProblem> {
    val status = HttpStatus.UNPROCESSABLE_CONTENT
    val body = ValidationProblem(ABOUT_BLANK, "Unprocessable Content", status.value(), null, code, errors)
    return ResponseEntity.status(status).contentType(PROBLEM).body(body)
}

internal fun invalidResponse(
    field: String,
    message: String,
) = unprocessableResponse(ErrorCode.VALIDATION_FAILED, listOf(FieldError(field, message)))

internal fun conflictResponse(body: Any): ResponseEntity<Any> {
    val conflict = ResponseEntity.status(HttpStatus.CONFLICT).contentType(PROBLEM)
    return conflict.body(body)
}

internal fun aboutBlank(): String = ABOUT_BLANK
