// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import java.util.UUID

// What the *Api implementations throw so that ApiExceptionHandler can answer with the problem of the
// contract. Domain exceptions (runs/RunErrors.kt, enrollment) are mapped there as well; these are only
// the ones with no domain of their own.

/** No such object in the session's tenant, or an id that cannot name one: 404 not_found. */
class ResourceNotFound : RuntimeException()

/** A request value is unacceptable: 422 validation_failed with an error at [field] ("" for the body as a whole). */
class RequestInvalid(
    val field: String,
    message: String,
) : RuntimeException(message)

/** A request is refused with a 422 of its own [code] and the [errors] that name the fields. */
class RequestRefused(
    val code: ErrorCode,
    val errors: List<FieldError>,
) : RuntimeException(code.name)

/** A token cannot be revoked (409 token_used with its [agentId], or token_expired). */
class TokenConflict(
    val code: ErrorCode,
    val agentId: UUID?,
) : RuntimeException(code.name)

/** A source cannot be started for a reason other than an active run (409 with the [code]). */
class RunRefused(
    val code: ErrorCode,
) : RuntimeException(code.name)
