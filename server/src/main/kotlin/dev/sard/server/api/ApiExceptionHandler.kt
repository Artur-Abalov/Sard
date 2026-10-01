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
import jakarta.persistence.PersistenceException
import org.hibernate.HibernateException
import org.slf4j.LoggerFactory
import org.springframework.core.Ordered
import org.springframework.core.annotation.Order
import org.springframework.dao.DataAccessException
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.http.ResponseEntity
import org.springframework.http.converter.HttpMessageNotReadableException
import org.springframework.transaction.TransactionException
import org.springframework.web.bind.annotation.ExceptionHandler
import org.springframework.web.bind.annotation.PathVariable
import org.springframework.web.bind.annotation.RestControllerAdvice
import org.springframework.web.method.annotation.MethodArgumentTypeMismatchException
import tools.jackson.core.JacksonException
import java.sql.SQLException

private val log = LoggerFactory.getLogger(ApiExceptionHandler::class.java)
private val PROBLEM = MediaType.parseMediaType(PROBLEM_JSON)
private const val ABOUT_BLANK = "about:blank"

/** Where a database message stops being safe to log: PostgreSQL quotes the failing row after it. */
private const val ROW_DETAIL = "Detail:"

/**
 * One answer per kind of failure, in the contract's problem shapes (S8b, "Ошибки — problem+json с кодом
 * контракта"). Every body is built from the [Problem] classes, so the OpenAPI document and the wire agree.
 * What a request carried (a config, a token) never goes into a title, a detail or a log line from here.
 */
@RestControllerAdvice
@Order(Ordered.HIGHEST_PRECEDENCE)
class ApiExceptionHandler {
    @ExceptionHandler(ResourceNotFound::class)
    fun notFound(): ResponseEntity<Problem> = problem(HttpStatus.NOT_FOUND, "Not Found", ErrorCode.NOT_FOUND)

    @ExceptionHandler(RequestInvalid::class)
    fun invalid(e: RequestInvalid): ResponseEntity<ValidationProblem> =
        unprocessable(ErrorCode.VALIDATION_FAILED, listOf(FieldError(e.field, e.message.orEmpty())))

    @ExceptionHandler(RequestRefused::class)
    fun refused(e: RequestRefused): ResponseEntity<ValidationProblem> = unprocessable(e.code, e.errors)

    /** The domain names the field "ttl"; the contract's is ttlSeconds. */
    @ExceptionHandler(EnrollmentTokenValidationException::class)
    fun tokenInvalid(e: EnrollmentTokenValidationException): ResponseEntity<ValidationProblem> {
        val field = if (e.field == "ttl") "ttlSeconds" else e.field
        return unprocessable(ErrorCode.VALIDATION_FAILED, listOf(FieldError(field, e.message.orEmpty())))
    }

    @ExceptionHandler(SourceNotFound::class)
    fun sourceNotFound(): ResponseEntity<Problem> = notFound()

    @ExceptionHandler(UnknownAgent::class)
    fun unknownAgent() = refused(ErrorCode.UNKNOWN_AGENT, "agentId", "names no agent of this tenant")

    @ExceptionHandler(AgentRevoked::class)
    fun agentRevoked() = refused(ErrorCode.AGENT_REVOKED, "agentId", "names a revoked agent")

    @ExceptionHandler(UnknownPlugin::class)
    fun unknownPlugin() = refused(ErrorCode.UNKNOWN_PLUGIN, "plugin", "is not offered by the agent")

    @ExceptionHandler(UnknownRepository::class)
    fun unknownRepository() = refused(ErrorCode.UNKNOWN_REPOSITORY, "repositoryName", "is not a repository of the agent")

    @ExceptionHandler(SourceNameTaken::class)
    fun nameTaken() = refused(ErrorCode.VALIDATION_FAILED, "name", "is taken by another source")

    @ExceptionHandler(InvalidConfig::class)
    fun invalidConfig(e: InvalidConfig) = unprocessable(ErrorCode.INVALID_CONFIG, e.violations.map { FieldError(it.field, it.message) })

    private fun refused(
        code: ErrorCode,
        field: String,
        message: String,
    ) = unprocessable(code, listOf(FieldError(field, message)))

    @ExceptionHandler(RunActive::class)
    fun runActive(e: RunActive): ResponseEntity<RunActiveProblem> {
        val status = HttpStatus.CONFLICT
        val body = RunActiveProblem(ABOUT_BLANK, "Conflict", status.value(), null, ErrorCode.RUN_ACTIVE, e.activeRunId)
        return ResponseEntity.status(status).contentType(PROBLEM).body(body)
    }

    @ExceptionHandler(RunRefused::class)
    fun runRefused(e: RunRefused): ResponseEntity<Problem> = problem(HttpStatus.CONFLICT, "Conflict", e.code)

    @ExceptionHandler(TokenConflict::class)
    fun tokenConflict(e: TokenConflict): ResponseEntity<TokenConflictProblem> {
        val body = TokenConflictProblem(ABOUT_BLANK, "Conflict", HttpStatus.CONFLICT.value(), null, e.code, e.agentId)
        return ResponseEntity.status(HttpStatus.CONFLICT).contentType(PROBLEM).body(body)
    }

    /** An id in the path that is no UUID names nothing (404); any other parameter that does not parse is 422. */
    @ExceptionHandler(MethodArgumentTypeMismatchException::class)
    fun mismatch(e: MethodArgumentTypeMismatchException): ResponseEntity<*> =
        if (e.parameter.hasParameterAnnotation(PathVariable::class.java)) {
            notFound()
        } else {
            invalid(RequestInvalid(e.name, "has an unacceptable value"))
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
        return invalid(RequestInvalid(field, "is missing or has an unacceptable value"))
    }

    /** The database is the only thing the API cannot work without: 503, with the reason in the log only. */
    @ExceptionHandler(
        HibernateException::class,
        PersistenceException::class,
        DataAccessException::class,
        TransactionException::class,
        SQLException::class,
    )
    fun unavailable(e: Exception): ResponseEntity<Problem> {
        log.error("database unavailable: {}: {}", e.javaClass.simpleName, safeMessage(e))
        return problem(HttpStatus.SERVICE_UNAVAILABLE, "Service Unavailable", ErrorCode.UNAVAILABLE)
    }

    private fun safeMessage(e: Throwable): String {
        val root = generateSequence(e) { it.cause?.takeIf { cause -> cause !== it } }.last()
        return root.message
            .orEmpty()
            .substringBefore(ROW_DETAIL)
            .trim()
    }

    private fun problem(
        status: HttpStatus,
        title: String,
        code: ErrorCode,
    ): ResponseEntity<Problem> =
        ResponseEntity.status(status).contentType(PROBLEM).body(Problem(ABOUT_BLANK, title, status.value(), null, code))

    private fun unprocessable(
        code: ErrorCode,
        errors: List<FieldError>,
    ): ResponseEntity<ValidationProblem> {
        val status = HttpStatus.UNPROCESSABLE_CONTENT
        val body = ValidationProblem(ABOUT_BLANK, "Unprocessable Content", status.value(), null, code, errors)
        return ResponseEntity.status(status).contentType(PROBLEM).body(body)
    }
}
