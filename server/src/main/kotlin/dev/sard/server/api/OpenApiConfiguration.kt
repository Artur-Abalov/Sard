// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import io.swagger.v3.oas.models.Components
import io.swagger.v3.oas.models.OpenAPI
import io.swagger.v3.oas.models.info.Info
import io.swagger.v3.oas.models.info.License
import io.swagger.v3.oas.models.media.Content
import io.swagger.v3.oas.models.media.MediaType
import io.swagger.v3.oas.models.media.Schema
import io.swagger.v3.oas.models.responses.ApiResponse
import io.swagger.v3.oas.models.security.SecurityRequirement
import io.swagger.v3.oas.models.security.SecurityScheme
import io.swagger.v3.oas.models.servers.Server
import org.springdoc.core.customizers.OpenApiCustomizer
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import kotlin.reflect.KClass
import kotlin.reflect.full.primaryConstructor

/** Name of the administrator session cookie (D2). */
const val SESSION_COOKIE = "sard_session"

private const val SESSION_SCHEME = "session"

/**
 * OpenAPI metadata; the web client is generated from /v3/api-docs. Every
 * operation needs the session cookie unless it declares an empty security
 * requirement (sign-in, status).
 */
@Configuration(proxyBeanMethods = false)
class OpenApiConfiguration {
    @Bean
    fun sardOpenApi(): OpenAPI =
        OpenAPI()
            .info(
                Info()
                    .title("Sard API")
                    .version("v1")
                    .license(License().name("AGPL-3.0-only").identifier("AGPL-3.0-only")),
            )
            // The console is served from the API's own origin.
            .servers(listOf(Server().url("/")))
            .components(
                Components().addSecuritySchemes(
                    SESSION_SCHEME,
                    SecurityScheme()
                        .type(SecurityScheme.Type.APIKEY)
                        .`in`(SecurityScheme.In.COOKIE)
                        .name(SESSION_COOKIE),
                ),
            ).addSecurityItem(SecurityRequirement().addList(SESSION_SCHEME))

    @Bean
    fun unauthorizedResponses(): OpenApiCustomizer = OpenApiCustomizer(::describeUnauthorized)

    @Bean
    fun originRejectedResponses(): OpenApiCustomizer = OpenApiCustomizer(::describeOriginRejected)

    @Bean
    fun unavailableResponses(): OpenApiCustomizer = OpenApiCustomizer(::describeUnavailable)

    @Bean
    fun requiredProperties(): OpenApiCustomizer = OpenApiCustomizer(::requireConstructorParameters)
}

/** Adds 401 to every operation that inherits the root security requirement. */
fun describeUnauthorized(api: OpenAPI) {
    val problem = Content().addMediaType(PROBLEM_JSON, MediaType().schema(Schema<Any>().`$ref`("Problem")))
    val unauthorized = ApiResponse().description("No session or it expired").content(problem)
    api.paths
        .orEmpty()
        .values
        .flatMap { it.readOperations() }
        .filter { it.security == null }
        .forEach { it.responses.addApiResponse("401", unauthorized) }
}

/** Adds 403 origin_rejected to every mutating operation (POST, PUT, PATCH, DELETE), K1. */
fun describeOriginRejected(api: OpenAPI) {
    val problem = Content().addMediaType(PROBLEM_JSON, MediaType().schema(Schema<Any>().`$ref`("Problem")))
    val rejected = ApiResponse().description("Origin does not match the request (CSRF)").content(problem)
    api.paths.orEmpty().values.forEach { item ->
        listOfNotNull(item.post, item.put, item.patch, item.delete).forEach { operation ->
            operation.responses.addApiResponse("403", rejected)
        }
    }
}

/** Adds 503 unavailable to every operation under /api/v1: the database is down (S8b В14, К11). */
fun describeUnavailable(api: OpenAPI) {
    val problem = Content().addMediaType(PROBLEM_JSON, MediaType().schema(Schema<Any>().`$ref`("Problem")))
    val unavailable = ApiResponse().description("The database is unavailable; nothing was changed").content(problem)
    api.paths
        .orEmpty()
        .filterKeys { it.startsWith("/api/v1/") }
        .values
        .flatMap { it.readOperations() }
        .forEach { it.responses.addApiResponse("503", unavailable) }
}

/**
 * Kotlin nullability does not reach springdoc (its model resolver reads classes
 * through Jackson 2, the Kotlin module here is for Jackson 3). A property is
 * required when its constructor parameter has no default; a nullable one is then
 * present and null. Schemas are matched to classes of this package by name.
 */
fun requireConstructorParameters(api: OpenAPI) {
    for ((name, schema) in objectSchemas(api)) {
        apiClass(name)?.let { schema.required = requiredParameters(it) }
    }
}

private fun objectSchemas(api: OpenAPI): Map<String, Schema<*>> =
    api.components
        ?.schemas
        .orEmpty()
        .filterValues { !it.properties.isNullOrEmpty() }

/** Names of the primary constructor's parameters without a default; null when there are none. */
private fun requiredParameters(type: KClass<*>): List<String>? =
    type.primaryConstructor
        ?.parameters
        .orEmpty()
        .filterNot { it.isOptional }
        .mapNotNull { it.name }
        .ifEmpty { null }

private fun apiClass(name: String): KClass<*>? =
    runCatching { Class.forName("${OpenApiConfiguration::class.java.packageName}.$name").kotlin }.getOrNull()
