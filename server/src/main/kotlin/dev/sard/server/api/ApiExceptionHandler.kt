// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.enrollment.EnrollmentTokenValidationException
import dev.sard.server.runs.AgentRevoked
import dev.sard.server.runs.InvalidConfig
import dev.sard.server.runs.RunActive
import dev.sard.server.runs.SourceNameTaken
import dev.sard.server.runs.SourceNotFound
import dev.sard.server.runs.UnknownAgent
import dev.sard.server.runs.UnknownPlugin
import dev.sard.server.runs.UnknownRepository
import dev.sard.server.scheduler.InvalidSchedule
import jakarta.persistence.PersistenceException
import org.hibernate.HibernateException
import org.slf4j.LoggerFactory
import org.springframework.core.Ordered
import org.springframework.core.annotation.Order
import org.springframework.dao.DataAccessException
import org.springframework.http.HttpStatus
import org.springframework.http.ResponseEntity
import org.springframework.http.converter.HttpMessageNotReadableException
import org.springframework.transaction.TransactionException
import org.springframework.web.bind.annotation.ExceptionHandler
import org.springframework.web.bind.annotation.PathVariable
import org.springframework.web.bind.annotation.RestControllerAdvice
import org.springframework.web.method.annotation.MethodArgumentTypeMismatchException
import tools.jackson.core.JacksonException
import java.sql.SQLException

private val log = LoggerFactory.getLogger(DatabaseExceptionHandler::class.java)

/** Where a database message stops being safe to log: PostgreSQL quotes the failing row after it. */
private const val ROW_DETAIL = "Detail:"

// One answer per kind of failure, in the contract's problem shapes (see ProblemResponses.kt). The advice
// classes are split by what they know about: the request, tokens, sources and runs, the database.

/** What is wrong with the request itself: no such object, a value that is not acceptable. */
@RestControllerAdvice
@Order(Ordered.HIGHEST_PRECEDENCE)
class RequestExceptionHandler {
    @ExceptionHandler(ResourceNotFound::class)
    fun notFound(): ResponseEntity<Problem> = notFoundResponse()

    @ExceptionHandler(RequestInvalid::class)
    fun invalid(e: RequestInvalid): ResponseEntity<ValidationProblem> = invalidResponse(e.field, e.message.orEmpty())

    @ExceptionHandler(RequestRefused::class)
    fun refused(e: RequestRefused): ResponseEntity<ValidationProblem> = unprocessableResponse(e.code, e.errors)

    /** An id in the path that is no UUID names nothing (404); any other parameter that does not parse is 422. */
    @ExceptionHandler(MethodArgumentTypeMismatchException::class)
    fun mismatch(e: MethodArgumentTypeMismatchException): ResponseEntity<*> =
        if (e.parameter.hasParameterAnnotation(PathVariable::class.java)) {
            notFoundResponse()
        } else {
            invalidResponse(e.name, "has an unacceptable value")
        }

    /** A body that does not read: 422 at the field Jackson stopped at, or at "" when it is not even JSON. */
    @ExceptionHandler(HttpMessageNotReadableException::class)
    fun unreadable(e: HttpMessageNotReadableException): ResponseEntity<ValidationProblem> {
        val field =
            generateSequence<Throwable>(e) { it.cause }
                .filterIsInstance<JacksonException>()
                .firstOrNull()
                ?.path
                ?.firstOrNull()
                ?.propertyName
                .orEmpty()
        return invalidResponse(field, "is missing or has an unacceptable value")
    }
}

/** Enrollment tokens: a value out of range, a token that cannot be revoked. */
@RestControllerAdvice
@Order(Ordered.HIGHEST_PRECEDENCE)
class TokenExceptionHandler {
    /** The domain names the field "ttl"; the contract's is ttlSeconds. */
    @ExceptionHandler(EnrollmentTokenValidationException::class)
    fun invalid(e: EnrollmentTokenValidationException): ResponseEntity<ValidationProblem> =
        invalidResponse(if (e.field == "ttl") "ttlSeconds" else e.field, e.message.orEmpty())

    @ExceptionHandler(TokenConflict::class)
    fun conflict(e: TokenConflict): ResponseEntity<Any> =
        conflictResponse(
            TokenConflictProblem(aboutBlank(), "Conflict", HttpStatus.CONFLICT.value(), null, e.code, e.agentId),
        )
}

/** Sources and runs: what the domain refuses, field by field. */
@RestControllerAdvice
@Order(Ordered.HIGHEST_PRECEDENCE)
class SourceExceptionHandler {
    @ExceptionHandler(SourceNotFound::class)
    fun notFound(): ResponseEntity<Problem> = notFoundResponse()

    @ExceptionHandler(UnknownAgent::class)
    fun unknownAgent() = refused(ErrorCode.UNKNOWN_AGENT, "agentId", "names no agent of this tenant")

    @ExceptionHandler(AgentRevoked::class)
    fun agentRevoked() = refused(ErrorCode.AGENT_REVOKED, "agentId", "names a revoked agent")

    @ExceptionHandler(UnknownPlugin::class)
    fun unknownPlugin() = refused(ErrorCode.UNKNOWN_PLUGIN, "plugin", "is not offered by the agent")

    @ExceptionHandler(UnknownRepository::class)
    fun unknownRepository() = refused(ErrorCode.UNKNOWN_REPOSITORY, "repositoryName", "is not the agent's repository")

    @ExceptionHandler(SourceNameTaken::class)
    fun nameTaken() = refused(ErrorCode.VALIDATION_FAILED, "name", "is taken by another source")

    @ExceptionHandler(InvalidConfig::class)
    fun invalidConfig(e: InvalidConfig) =
        unprocessableResponse(ErrorCode.INVALID_CONFIG, e.violations.map { FieldError(it.field, it.message) })

    @ExceptionHandler(InvalidSchedule::class)
    fun invalidSchedule(e: InvalidSchedule): ResponseEntity<ValidationProblem> =
        invalidResponse(e.field.name.lowercase(), e.message.orEmpty())

    @ExceptionHandler(RunActive::class)
    fun runActive(e: RunActive): ResponseEntity<Any> =
        conflictResponse(
            RunActiveProblem(
                aboutBlank(),
                "Conflict",
                HttpStatus.CONFLICT.value(),
                null,
                ErrorCode.RUN_ACTIVE,
                e.activeRunId,
            ),
        )

    @ExceptionHandler(RunRefused::class)
    fun runRefused(e: RunRefused): ResponseEntity<Problem> = problemResponse(HttpStatus.CONFLICT, "Conflict", e.code)

    private fun refused(
        code: ErrorCode,
        field: String,
        message: String,
    ) = unprocessableResponse(code, listOf(FieldError(field, message)))
}

/** The database is the only thing the API cannot work without: 503, with the reason in the log only. */
@RestControllerAdvice
@Order(Ordered.HIGHEST_PRECEDENCE)
class DatabaseExceptionHandler {
    @ExceptionHandler(
        HibernateException::class,
        PersistenceException::class,
        DataAccessException::class,
        TransactionException::class,
        SQLException::class,
    )
    fun unavailable(e: Exception): ResponseEntity<Problem> {
        log.error("database unavailable: {}: {}", e.javaClass.simpleName, safeMessage(e))
        return problemResponse(HttpStatus.SERVICE_UNAVAILABLE, "Service Unavailable", ErrorCode.UNAVAILABLE)
    }

    private fun safeMessage(e: Throwable): String {
        val root = generateSequence(e) { it.cause?.takeIf { cause -> cause !== it } }.last()
        return root.message
            .orEmpty()
            .substringBefore(ROW_DETAIL)
            .trim()
    }
}
