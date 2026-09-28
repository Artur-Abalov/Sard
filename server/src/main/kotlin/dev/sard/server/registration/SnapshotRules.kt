// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.registration

import dev.sard.server.enrollment.Hostnames
import tools.jackson.core.JacksonException
import tools.jackson.databind.DeserializationFeature
import tools.jackson.databind.json.JsonMapper

private typealias Reason = RegistrationRejectedException.Reason

/** Plugin, repository, crypto provider, secret and script names: the one name format (S4a decision 5). */
private val NAME = Regex("^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")

/** Versions, os and arch: 1 to 64 printable ASCII characters, no spaces. */
private val LABEL = Regex("^[!-~]{1,64}$")

/** A restic backend scheme, as the agent derives it from the repository URL. */
private val BACKEND = Regex("^[a-z][a-z0-9]{0,15}$")

/** restic repository ids are 32 random bytes in lower-case hex. */
private val REPOSITORY_ID = Regex("^[0-9a-f]{64}$")

private val JSON = JsonMapper.builder().enable(DeserializationFeature.FAIL_ON_TRAILING_TOKENS).build()

/**
 * What Register accepts from an authenticated, possibly compromised host (S4a). The first
 * violation rejects the whole snapshot: it is a fault of the agent, and a partial snapshot
 * would misstate what the host can do. Checks run in field order, the protocol first.
 */
object SnapshotRules {
    const val MAX_PLUGINS = 64
    const val MAX_REPOSITORIES = 256
    const val MAX_SECRET_NAMES = 1024
    const val MAX_SCRIPT_NAMES = 1024
    const val MAX_CONFIG_SCHEMA_BYTES = 64 * 1024

    fun check(snapshot: AgentSnapshot) {
        checkProtocol(snapshot.protocolVersion)
        if (!Hostnames.isValid(snapshot.hostname)) throw rejected(Reason.HOSTNAME_INVALID, "hostname")
        checkLabel(snapshot.agentVersion, "agent_version")
        checkLabel(snapshot.os, "os")
        checkLabel(snapshot.arch, "arch")
        checkPlugins(snapshot.plugins)
        checkRepositories(snapshot.repositories)
        checkNames(snapshot.secretNames, "secret_names", MAX_SECRET_NAMES) { "secret_names[$it]" }
        checkNames(snapshot.scriptNames, "script_names", MAX_SCRIPT_NAMES) { "script_names[$it]" }
    }

    private fun checkProtocol(version: Long) {
        val supported = ProtocolVersions.SUPPORTED
        if (version in supported) return
        val range = mapOf("min_supported" to "${supported.first}", "max_supported" to "${supported.last}")
        throw RegistrationRejectedException(Reason.PROTOCOL_UNSUPPORTED, range)
    }

    private fun checkPlugins(plugins: List<PluginEntry>) {
        checkNames(plugins.map { it.name }, "plugins", MAX_PLUGINS) { "plugins[$it].name" }
        plugins.forEachIndexed { i, plugin ->
            checkLabel(plugin.version, "plugins[$i].version")
            checkConfigSchema(plugin.configSchema, "plugins[$i].config_schema")
            val actions = plugin.actions
            if (null in actions || actions.toSet().size != actions.size) throw invalid("plugins[$i].actions")
        }
    }

    private fun checkRepositories(repositories: List<RepositoryEntry>) {
        checkNames(repositories.map { it.name }, "repositories", MAX_REPOSITORIES) { "repositories[$it].name" }
        repositories.forEachIndexed { i, repository ->
            if (!BACKEND.matches(repository.backend)) throw invalid("repositories[$i].backend")
            checkOptional(repository.repositoryId, REPOSITORY_ID, "repositories[$i].repository_id")
            checkOptional(repository.cryptoProvider, NAME, "repositories[$i].crypto_provider")
        }
    }

    private fun checkNames(
        names: List<String>,
        collection: String,
        limit: Int,
        element: (Int) -> String,
    ) {
        checkCount(names.size, collection, limit)
        val seen = HashSet<String>()
        names.forEachIndexed { i, name ->
            if (!NAME.matches(name)) throw rejected(Reason.NAME_INVALID, element(i))
            if (!seen.add(name)) throw rejected(Reason.NAME_DUPLICATE, element(i))
        }
    }

    private fun checkCount(
        count: Int,
        field: String,
        limit: Int,
    ) {
        if (count > limit) throw tooLarge(field, limit)
    }

    private fun checkConfigSchema(
        schema: String,
        field: String,
    ) {
        checkCount(schema.toByteArray(Charsets.UTF_8).size, field, MAX_CONFIG_SCHEMA_BYTES)
        if (!storableJson(schema)) throw rejected(Reason.CONFIG_SCHEMA_INVALID, field)
    }
}

/** One JSON value and nothing after it; no NUL character, which PostgreSQL's jsonb cannot hold. */
private fun storableJson(text: String): Boolean =
    try {
        val tree = JSON.readTree(text)
        !tree.isMissingNode && !tree.toString().contains("\\u0000")
    } catch (_: JacksonException) {
        false
    }

private fun checkLabel(
    value: String,
    field: String,
) {
    if (!LABEL.matches(value)) throw invalid(field)
}

private fun checkOptional(
    value: String,
    format: Regex,
    field: String,
) {
    if (value.isNotEmpty() && !format.matches(value)) throw invalid(field)
}

private fun invalid(field: String) = rejected(Reason.FIELD_INVALID, field)

private fun tooLarge(
    field: String,
    limit: Int,
) = RegistrationRejectedException(Reason.SNAPSHOT_TOO_LARGE, mapOf("field" to field, "limit" to "$limit"))

private fun rejected(
    reason: Reason,
    field: String,
) = RegistrationRejectedException(reason, mapOf("field" to field))
