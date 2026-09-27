// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.annotations.Parameter
import io.swagger.v3.oas.annotations.media.Content
import io.swagger.v3.oas.annotations.media.Schema
import io.swagger.v3.oas.annotations.responses.ApiResponse

// Documentation shared by the endpoints of /api/v1. Pages are cursor-based: the
// client passes back nextCursor until it is null.

const val DEFAULT_LIMIT = "50"
const val CURSOR_NEXT = "Pass as cursor to get the next page; null on the last page"

@Target(AnnotationTarget.VALUE_PARAMETER)
@Retention(AnnotationRetention.RUNTIME)
@Parameter(description = "nextCursor of the previous page; omit for the first page")
annotation class PageCursor

@Target(AnnotationTarget.VALUE_PARAMETER)
@Retention(AnnotationRetention.RUNTIME)
@Parameter(
    description = "Page size",
    schema = Schema(type = "integer", format = "int32", minimum = "1", maximum = "200", defaultValue = DEFAULT_LIMIT),
)
annotation class PageLimit

@Target(AnnotationTarget.FUNCTION)
@Retention(AnnotationRetention.RUNTIME)
@ApiResponse(
    responseCode = "404",
    description = "Not found in the session's tenant",
    content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = Problem::class))],
)
annotation class NotFound

@Target(AnnotationTarget.FUNCTION)
@Retention(AnnotationRetention.RUNTIME)
@ApiResponse(
    responseCode = "422",
    description = "Rejected values; errors name the fields",
    content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = ValidationProblem::class))],
)
annotation class Unprocessable

@Target(AnnotationTarget.FUNCTION)
@Retention(AnnotationRetention.RUNTIME)
@ApiResponse(
    responseCode = "409",
    description = "The source has an active run (D6)",
    content = [Content(mediaType = PROBLEM_JSON, schema = Schema(implementation = RunActiveProblem::class))],
)
annotation class RunActive
