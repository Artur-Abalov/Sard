// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import ch.qos.logback.classic.Level
import ch.qos.logback.classic.spi.ILoggingEvent
import ch.qos.logback.core.read.ListAppender
import com.networknt.schema.Schema
import com.networknt.schema.SchemaRegistry
import com.networknt.schema.dialect.Dialects
import dev.sard.server.agents.dispatch.ReconciledHellos
import dev.sard.server.agents.dispatch.StepDispatcher
import dev.sard.server.auth.AdminSession
import dev.sard.server.extension.TenantResolver
import dev.sard.server.onboarding.CODE
import dev.sard.server.onboarding.SetupCodeGenerator
import dev.sard.server.pki.MovableClock
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.context.annotation.Primary
import org.springframework.web.context.request.RequestContextHolder
import org.springframework.web.context.request.ServletRequestAttributes
import tools.jackson.databind.JsonNode
import tools.jackson.databind.ObjectMapper
import java.net.URI
import java.net.http.HttpClient
import java.net.http.HttpRequest
import java.net.http.HttpResponse
import java.time.Instant
import java.time.temporal.ChronoUnit
import java.util.UUID

/**
 * "T0" of the specification. The spec names 2026-10-01T12:00:00Z; the tests start from the real
 * time at a whole second instead, because the server's certificates are issued at the clock and
 * the TLS handshake of the gRPC scenarios judges them by the real one.
 */
val T0: Instant = Instant.now().truncatedTo(ChronoUnit.SECONDS)

/** The setup code every REST test server prints; [ApiClient.signIn] enters it. */
const val WIZARD_CODE = CODE

/** The password [ApiClient.signIn] sets in the wizard, and signs in with afterwards. */
const val TEST_ADMIN_PASSWORD = "test-admin-password-2026"

/** The header a test sets on sign-in to name the session's tenant (the stand-in for an enterprise resolver). */
const val TEST_TENANT_HEADER = "X-Test-Tenant"

/**
 * Takes the tenant from the session of the request, as the enterprise resolver will (ADR 0013, OQ-035);
 * at sign-in, where there is no session yet, from [TEST_TENANT_HEADER].
 */
class SessionTenantResolver : TenantResolver {
    override fun currentTenantId(): UUID {
        val request = (RequestContextHolder.getRequestAttributes() as? ServletRequestAttributes)?.request
        val session = request?.getAttribute(SESSION_REQUEST_ATTRIBUTE) as? AdminSession
        val header = request?.getHeader(TEST_TENANT_HEADER)
        return session?.tenantId ?: header?.let(UUID::fromString) ?: TenantResolver.DEFAULT_TENANT_ID
    }
}

/** The code the wizard of [ApiClient.signIn] enters. */
@TestConfiguration(proxyBeanMethods = false)
class WizardCodeConfiguration {
    @Bean
    fun setupCodeGenerator(): SetupCodeGenerator = SetupCodeGenerator { WIZARD_CODE }
}

@TestConfiguration(proxyBeanMethods = false)
@Import(WizardCodeConfiguration::class)
class RestApiTestConfiguration {
    @Bean
    fun clock() = MovableClock(T0)

    @Bean
    @Primary
    fun sessionTenantResolver(): TenantResolver = SessionTenantResolver()

    /** The real dispatcher behind the extension point, telling the test when a Hello has been reconciled. */
    @Bean
    @Primary
    fun reconciledHellos(dispatcher: StepDispatcher) = ReconciledHellos(dispatcher)
}

/** The JSON of a source of the files plugin on repository "qa", as a client sends it. */
fun sourceJson(
    name: String,
    agentId: UUID,
    path: String = "/etc",
): String {
    val config = """{"paths":["$path"]}"""
    return """{"name":"$name","agentId":"$agentId","plugin":"files","repositoryName":"qa","config":$config}"""
}

/** The elements of a JSON array (Jackson's own `map` and `filter` are not Kotlin's). */
fun JsonNode.list(): List<JsonNode> = iterator().asSequence().toList()

/** [field] of every element of the array [name] of this node. */
fun JsonNode.pluck(
    name: String,
    field: String,
): List<String> = path(name).list().map { it.path(field).asString() }

/** A response of the API under test, already checked against the OpenAPI contract. */
class ApiResponse(
    val status: Int,
    val contentType: String?,
    val body: String,
    val json: JsonNode,
) {
    val code: String? get() = json.path("code").takeIf { !it.isMissingNode }?.asString()

    fun errorFields(): List<String> =
        json
            .path("errors")
            .iterator()
            .asSequence()
            .map { it.path("field").asString() }
            .toList()

    override fun toString() = "$status $contentType $body"
}

/** An administrator signed in on the server under test. */
class ApiSession(
    val cookie: String,
    val tenant: UUID,
)

/** Every response goes through [ContractCheck] before the test sees it (the rule "Ответы соответствуют контракту"). */
class ApiClient(
    private val port: Int,
    private val mapper: ObjectMapper,
) {
    private val http = HttpClient.newHttpClient()
    private val contract by lazy { ContractCheck(mapper.readTree(raw("GET", "/v3/api-docs").body()), mapper) }

    private fun raw(
        method: String,
        path: String,
        body: String? = null,
        headers: Map<String, String> = emptyMap(),
    ): HttpResponse<String> {
        val publisher = body?.let { HttpRequest.BodyPublishers.ofString(it) } ?: HttpRequest.BodyPublishers.noBody()
        val builder = HttpRequest.newBuilder(URI.create("http://localhost:$port$path")).method(method, publisher)
        if (body != null) builder.header("Content-Type", "application/json")
        headers.forEach { (name, value) -> builder.header(name, value) }
        return http.send(builder.build(), HttpResponse.BodyHandlers.ofString())
    }

    /**
     * Signs in as the test administrator of [tenant] (the default one unless told otherwise). A server that has
     * no administrator yet answers 409 setup_required: the wizard runs first, with the code of the test
     * (F4a), and the sign-in is repeated.
     */
    fun signIn(tenant: UUID = TenantResolver.DEFAULT_TENANT_ID): ApiSession {
        var response = login(tenant)
        if (response.statusCode() == 409) {
            runWizard()
            response = login(tenant)
        }
        val cookie = response.headers().firstValue("Set-Cookie").orElseThrow()
        return ApiSession(Regex("$SESSION_COOKIE=([^;]+)").find(cookie)!!.groupValues[1], tenant)
    }

    private fun login(tenant: UUID) =
        raw(
            "POST",
            "/api/v1/session",
            """{"password":"$TEST_ADMIN_PASSWORD"}""",
            mapOf(TEST_TENANT_HEADER to tenant.toString()),
        )

    /** Code, confirmation of the CA, administrator password: the first start, as an owner does it. */
    private fun runWizard() {
        val entered = raw("POST", "/api/v1/onboarding/setup-session", """{"code":"$WIZARD_CODE"}""")
        val setup = Regex("$SETUP_COOKIE=([^;]+)").find(entered.headers().firstValue("Set-Cookie").orElseThrow())!!
        val cookie = mapOf("Cookie" to "$SETUP_COOKIE=${setup.groupValues[1]}")
        check(raw("POST", "/api/v1/onboarding/ca", null, cookie).statusCode() == 204) { "the wizard refused the CA" }
        val admin = raw("POST", "/api/v1/onboarding/admin", """{"password":"$TEST_ADMIN_PASSWORD"}""", cookie)
        check(admin.statusCode() == 204) { "the wizard refused the password: ${admin.body()}" }
    }

    /** One request; [body] is JSON text, [session] null sends no cookie, [headers] are extra request headers. */
    fun send(
        method: String,
        path: String,
        session: ApiSession?,
        body: String? = null,
        headers: Map<String, String> = emptyMap(),
    ): ApiResponse {
        val cookie = session?.let { mapOf("Cookie" to "$SESSION_COOKIE=${it.cookie}") }.orEmpty()
        val response = raw(method, path, body, headers + cookie)
        val text = response.body()
        val json = if (text.isBlank()) mapper.nullNode() else runCatching { mapper.readTree(text) }.getOrNull()
        val checked =
            ApiResponse(
                response.statusCode(),
                response.headers().firstValue("Content-Type").orElse(null),
                text,
                json ?: mapper.nullNode(),
            )
        contract.check(method, path.substringBefore('?'), checked)
        return checked
    }

    fun get(
        path: String,
        session: ApiSession?,
    ) = send("GET", path, session)

    fun post(
        path: String,
        session: ApiSession?,
        body: String? = "{}",
    ) = send("POST", path, session, body)
}

/**
 * Checks a response against the document served at /v3/api-docs: the status is declared for the
 * operation, the content type is declared for the status, and the body is valid against the
 * schema of that content type (JSON Schema 2020-12, as OpenAPI 3.1 is).
 */
class ContractCheck(
    private val spec: JsonNode,
    private val mapper: ObjectMapper,
) {
    private val registry =
        SchemaRegistry.withDialect(Dialects.getDraft202012()) { builder ->
            builder.schemaRegistryConfig(
                com.networknt.schema.SchemaRegistryConfig
                    .builder()
                    .formatAssertionsEnabled(true)
                    .build(),
            )
        }
    private val templates: Map<Regex, String> =
        spec.path("paths").propertyNames().associateBy { template ->
            Regex(template.replace(Regex("\\{[^/}]+}"), "[^/]+"))
        }

    fun check(
        method: String,
        path: String,
        response: ApiResponse,
    ) {
        val template = templates.entries.firstOrNull { it.key.matches(path) }?.value
        require(template != null) { "$method $path: no such path in the OpenAPI document ($response)" }
        val operation = spec.path("paths").path(template).path(method.lowercase())
        require(!operation.isMissingNode) { "$method $template: not an operation of the contract ($response)" }
        val declared = operation.path("responses")
        val description =
            declared.path(response.status.toString()).takeIf { !it.isMissingNode }
                ?: declared.path("default").takeIf { !it.isMissingNode }
        require(description != null) {
            "$method $template: status ${response.status} is not declared (${declared.propertyNames()}); $response"
        }
        val content = description.path("content")
        if (response.body.isBlank()) {
            require(content.isMissingNode || content.isEmpty) { "$method $template: ${response.status} has no body" }
            return
        }
        val mediaType = response.contentType?.substringBefore(';')?.trim()
        val media = content.path(mediaType ?: "")
        require(!media.isMissingNode) { "$method $template: content type $mediaType is not declared; $response" }
        val schema = media.path("schema")
        if (schema.isMissingNode) return
        val errors = schemaOf(schema).validate(response.json)
        require(errors.isEmpty()) {
            "$method $template: ${response.status} body is not valid against its schema: " +
                errors.joinToString("; ") { it.message } + "; $response"
        }
    }

    private fun schemaOf(schema: JsonNode): Schema {
        val root = mapper.createObjectNode()
        root.put("\$schema", "https://json-schema.org/draft/2020-12/schema")
        root.set("components", spec.path("components"))
        root.putArray("allOf").add(schema)
        return registry.getSchema(root)
    }
}

/** The log of the whole server (every logger, DEBUG) while [block] runs, as the lines a reader would see. */
fun captureLogs(block: () -> Unit): List<String> {
    val root = org.slf4j.LoggerFactory.getLogger(org.slf4j.Logger.ROOT_LOGGER_NAME) as ch.qos.logback.classic.Logger
    val appender = ListAppender<ILoggingEvent>()
    appender.start()
    val level = root.level
    root.level = Level.DEBUG
    root.addAppender(appender)
    try {
        block()
    } finally {
        root.detachAppender(appender)
        appender.stop()
        root.level = level
    }
    return appender.list.flatMap { event ->
        listOfNotNull("[${event.loggerName}] ${event.formattedMessage}") +
            generateSequence(event.throwableProxy) { it.cause }.mapNotNull { it.message }
    }
}
