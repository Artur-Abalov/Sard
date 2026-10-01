// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import com.networknt.schema.Schema
import com.networknt.schema.SchemaException
import com.networknt.schema.SchemaRegistry
import com.networknt.schema.SchemaRegistryConfig
import com.networknt.schema.dialect.Dialect
import com.networknt.schema.dialect.Dialects
import com.networknt.schema.format.Format
import com.networknt.schema.path.PathType
import tools.jackson.core.JacksonException
import tools.jackson.databind.JsonNode
import tools.jackson.databind.json.JsonMapper

private val JSON = JsonMapper.builder().build()
private const val SECRET_FORMAT = "sard-secret"
private const val ROOT = "config"
private const val BROKEN_SCHEMA = "the plugin's config schema is not a valid JSON Schema"

/** `"format": "sard-secret"` marks a field that holds the name of a secret defined on the agent host (ADR 0027). */
private class SecretName(
    private val known: Set<String>,
) : Format {
    override fun getName() = SECRET_FORMAT

    override fun matches(
        executionContext: com.networknt.schema.ExecutionContext,
        value: String,
    ) = value in known
}

/**
 * Checks a source's config against the config schema the agent announced (S8b В15): JSON Schema
 * 2020-12, all violations at once, each named by `config` plus its JSON Pointer, as the agent names
 * them. A string marked `sard-secret` must be one of the agent's secret names. Keywords the validator
 * does not know (`x-sard-i18n`) are ignored. What only the agent can judge (a path inside another path)
 * is not checked here. A message names the rule and a secret's name, never a value of the config.
 */
internal object ConfigCheck {
    fun violations(
        schema: String,
        config: String,
        secretNames: Set<String>,
    ): List<ConfigViolation> {
        val compiled = compile(schema, secretNames) ?: return listOf(ConfigViolation(ROOT, BROKEN_SCHEMA))
        return compiled
            .validate(JSON.readTree(config))
            .map { ConfigViolation(field(it.instanceLocation.toString()), message(it)) }
            .sortedWith(compareBy({ it.field }, { it.message }))
    }

    private fun compile(
        schema: String,
        secretNames: Set<String>,
    ): Schema? {
        val config =
            SchemaRegistryConfig
                .builder()
                .formatAssertionsEnabled(true)
                .pathType(PathType.JSON_POINTER)
                .build()
        val dialect: Dialect = Dialect.builder(Dialects.getDraft202012()).format(SecretName(secretNames)).build()
        return try {
            val node: JsonNode = JSON.readTree(schema)
            SchemaRegistry
                .withDialect(dialect) {
                    it.schemaRegistryConfig(config).schemaLoader(::noLoading)
                }.getSchema(node)
                .also { it.initializeValidators() }
        } catch (_: JacksonException) {
            null
        } catch (_: SchemaException) {
            null
        }
    }

    /** The schema comes from an agent host, which may be compromised: a `$ref` resolves inside the schema only. */
    private fun noLoading(loader: com.networknt.schema.resource.SchemaLoader.Builder) {
        loader.fetchRemoteResources(false)
        loader.resourceLoaders { it.values { loaders -> loaders.clear() } }
    }

    private fun field(pointer: String) = ROOT + pointer

    private fun message(error: com.networknt.schema.Error): String =
        if (error.keyword == "format" && error.schemaNode.asString() == SECRET_FORMAT) {
            "unknown secret \"${error.instanceNode.asString()}\""
        } else {
            "violates \"${error.keyword}\" of the schema"
        }
}
