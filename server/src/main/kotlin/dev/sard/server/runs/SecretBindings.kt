// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import tools.jackson.core.JacksonException
import tools.jackson.databind.JsonNode
import tools.jackson.databind.json.JsonMapper
import java.net.InetAddress
import java.nio.file.Path

private val JSON = JsonMapper.builder().build()
private const val SECRET_FORMAT = "sard-secret"

/** The keyword of a config schema that marks the host, the port and the database of a plugin's target (S11). */
private const val TARGET_KEYWORD = "x-sard-target"
private const val ANY = "*"

/**
 * Where the value of a secret goes: the server a plugin signs in to, as the agent names it (sdk.Target).
 * [port] 0 and [database] "" when the plugin has none.
 */
internal data class SecretTarget(
    val host: String,
    val port: Int,
    val database: String,
) {
    /** host[:port][/database], an IPv6 address in brackets, as the operator binds it. */
    override fun toString(): String {
        val host = if (port != 0 && ':' in host && !host.startsWith("/")) "[$host]" else host
        return host + (if (port != 0) ":$port" else "") + (if (database.isNotEmpty()) "/$database" else "")
    }
}

/**
 * The bindings an agent reported (S11, agents.secret_bindings), judged against the config of a source before it
 * runs. The agent decides; this only warns about what it will refuse. The grammar of a target is the agent's
 * (agent/internal/secretbind): host[:port][/database], [ipv6][:port][/database], /dir[:port[/database]]; a port or
 * a database left out, or *, is any; a host name is compared without regard to case, an address by its value.
 */
internal object SecretBindings {
    /**
     * What the agent would refuse of [config] of [plugin] (its [schema] marks the secrets and the target): one line
     * per secret, with the command the operator of the host runs. A secret the agent reported no binding for at all
     * (an agent older than S11) gives none.
     */
    fun warnings(
        plugin: String,
        schema: String,
        config: String,
        bindings: String,
    ): List<String> {
        val properties = read(schema)?.get("properties") ?: return emptyList()
        val values = read(config) ?: return emptyList()
        val target = targetOf(properties, values)
        val reported = read(bindings)?.associateBy { it.path("name").asString("") }.orEmpty()
        return secretNames(properties, values).mapNotNull { name ->
            reported[name]?.let { warning(name, plugin, target, it) }
        }
    }

    private fun warning(
        name: String,
        plugin: String,
        target: SecretTarget?,
        binding: JsonNode,
    ): String? {
        val fix = "run on the agent host: sudo sard-agent secret set $name --plugin $plugin --target ${target ?: "<target>"}"
        val problem = binding.path("problem").asString("")
        val boundTo = binding.path("plugin").asString("")
        return when {
            problem.isNotEmpty() -> "secret \"$name\" is given to no plugin on the agent host ($problem); $fix"
            boundTo != plugin -> "secret \"$name\" is bound to the plugin $boundTo, not $plugin; $fix"
            target == null || binding.path("targets").any { matches(it.asString(""), target) } -> null
            else -> "secret \"$name\" is not allowed for $plugin at $target; if that is intended, $fix"
        }
    }

    /** The names of the secrets the config holds in the fields its schema marks sard-secret. */
    private fun secretNames(
        properties: JsonNode,
        values: JsonNode,
    ): List<String> =
        properties.properties().mapNotNull { (field, property) ->
            values.get(field)?.takeIf { property.path("format").asString("") == SECRET_FORMAT && it.isString }?.asString()
        }

    /** The target of the config, if its schema marks a host. */
    private fun targetOf(
        properties: JsonNode,
        values: JsonNode,
    ): SecretTarget? {
        val fields = properties.properties().associate { (field, property) -> property.path(TARGET_KEYWORD).asString("") to field }
        val host = fields["host"]?.let { values.get(it)?.asString() } ?: return null
        val port = fields["port"]?.let { values.get(it) ?: properties.get(it).get("default") }?.asInt(0) ?: 0
        val database = fields["database"]?.let { values.get(it)?.asString() }.orEmpty()
        return SecretTarget(host, port, database)
    }

    /** Whether [target] is a target of [pattern]; a pattern the agent would not write matches nothing. */
    fun matches(
        pattern: String,
        target: SecretTarget,
    ): Boolean {
        val parts = parse(pattern) ?: return false
        return sameHost(parts.host, target.host) &&
            any(parts.port, target.port.toString()) &&
            any(parts.database, target.database)
    }

    private data class Parts(
        val host: String,
        val port: String,
        val database: String,
    )

    private fun parse(pattern: String): Parts? =
        when {
            pattern.startsWith("/") -> socket(pattern)
            pattern.startsWith("[") -> bracketed(pattern)
            ipv6(pattern) -> Parts(pattern, "", "")
            else -> tail(pattern.substringBefore(':').substringBefore('/'), pattern.dropWhile { it != ':' && it != '/' })
        }

    private fun socket(pattern: String): Parts =
        if (':' in pattern) tail(pattern.substringBeforeLast(':'), ":" + pattern.substringAfterLast(':')) else Parts(pattern, "", "")

    private fun bracketed(pattern: String): Parts? {
        val end = pattern.indexOf(']')
        return if (end < 0) null else tail(pattern.substring(1, end), pattern.substring(end + 1))
    }

    /** [rest] is [:port][/database] after [host]. */
    private fun tail(
        host: String,
        rest: String,
    ): Parts =
        if (rest.startsWith(":")) {
            Parts(host, rest.drop(1).substringBefore('/'), rest.drop(1).substringAfter('/', ""))
        } else {
            Parts(host, "", rest.removePrefix("/"))
        }

    private fun any(
        pattern: String,
        value: String,
    ) = pattern.isEmpty() || pattern == ANY || pattern == value

    private fun sameHost(
        pattern: String,
        host: String,
    ): Boolean {
        val address = literal(pattern)
        return when {
            pattern.startsWith("/") -> Path.of(pattern).normalize() == Path.of(host).normalize()
            address != null -> address == literal(host)
            else -> pattern.equals(host, ignoreCase = true)
        }
    }

    /** The address a literal stands for, null for a name: never a lookup in DNS. */
    private fun literal(host: String): InetAddress? =
        try {
            InetAddress.ofLiteral(host)
        } catch (_: IllegalArgumentException) {
            null
        }

    private fun ipv6(host: String) = ':' in host && literal(host) != null

    private fun read(text: String): JsonNode? =
        try {
            JSON.readTree(text)
        } catch (_: JacksonException) {
            null
        }
}
