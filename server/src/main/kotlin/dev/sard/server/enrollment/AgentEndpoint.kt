// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.pki.ServerNames

private const val MIN_PORT = 1
private const val MAX_PORT = 65535

/** The address agents dial to reach this server (decision 5): host:port as it goes into the enroll command. */
data class AgentEndpoint(
    val address: String,
) {
    fun enrollCommand(token: String): String = "sard-agent enroll --server $address --token $token"
}

/** SARD_AGENT_ENDPOINT does not parse, or its host is not covered by sard.pki.server-names. */
class InvalidAgentEndpointException(
    message: String,
) : IllegalStateException(message)

private fun fail(text: String): Nothing =
    throw InvalidAgentEndpointException("SARD_AGENT_ENDPOINT is not a valid host or host:port: '$text'")

private fun parsePort(text: String): Int? = text.toIntOrNull()?.takeIf { it in MIN_PORT..MAX_PORT }

/** Host, its as-given prefix (with brackets for IPv6) and port text, or null if the shape is unknown. */
private data class AddressShape(
    val host: String,
    val prefix: String,
    val portText: String?,
)

private fun bracketedShape(text: String): AddressShape? {
    val end = text.indexOf(']')
    if (end < 0) return null
    val host = text.substring(1, end)
    val rest = text.substring(end + 1)
    return when {
        rest.isEmpty() -> AddressShape(host, "[$host]", null)
        rest.startsWith(':') -> AddressShape(host, "[$host]", rest.substring(1))
        else -> null
    }
}

private fun hostColonPortShape(text: String): AddressShape? =
    when (text.count { it == ':' }) {
        0 -> {
            AddressShape(text, text, null)
        }

        1 -> {
            val idx = text.indexOf(':')
            val host = text.substring(0, idx)
            AddressShape(host, host, text.substring(idx + 1))
        }

        // A bare IPv6 literal has more than one colon and needs brackets to carry a port unambiguously.
        else -> {
            null
        }
    }

private fun shapeOf(text: String): AddressShape? {
    val bracketed = text.startsWith('[')
    return if (bracketed) bracketedShape(text) else hostColonPortShape(text)
}

private fun resolvedPort(
    portText: String?,
    grpcPort: Int,
): Int? = if (portText == null) grpcPort else parsePort(portText)

/** Resolves and validates SARD_AGENT_ENDPOINT against the server certificate's names (decision 5). */
object AgentEndpointResolver {
    /** [configured] null or empty means unset: the first of [serverNames] and [grpcPort] are used. */
    fun resolve(
        configured: String?,
        serverNames: List<String>,
        grpcPort: Int,
    ): AgentEndpoint {
        require(serverNames.isNotEmpty()) { "sard.pki.server-names must not be empty" }
        if (configured.isNullOrEmpty()) return default(serverNames, grpcPort)
        return explicit(configured, serverNames, grpcPort)
    }

    private fun default(
        serverNames: List<String>,
        grpcPort: Int,
    ): AgentEndpoint {
        val first = serverNames.first()
        val prefix = if (first.contains(':')) "[$first]" else first
        return AgentEndpoint("$prefix:$grpcPort")
    }

    private fun explicit(
        text: String,
        serverNames: List<String>,
        grpcPort: Int,
    ): AgentEndpoint {
        val parsed = parseHostPort(text, grpcPort)
        if (!covered(parsed.hostForMatching, serverNames)) {
            throw InvalidAgentEndpointException(
                "SARD_AGENT_ENDPOINT host '${parsed.hostForMatching}' is not covered by any of " +
                    "sard.pki.server-names: ${serverNames.joinToString()}",
            )
        }
        return AgentEndpoint("${parsed.prefixAsGiven}:${parsed.port}")
    }

    private fun covered(
        host: String,
        serverNames: List<String>,
    ): Boolean = ServerNames.covers(host, serverNames)

    private data class ParsedAddress(
        val hostForMatching: String,
        val prefixAsGiven: String,
        val port: Int,
    )

    private fun parseHostPort(
        text: String,
        grpcPort: Int,
    ): ParsedAddress {
        if ("://" in text) fail(text)
        return tryParse(text, grpcPort) ?: fail(text)
    }

    private fun tryParse(
        text: String,
        grpcPort: Int,
    ): ParsedAddress? {
        val shape = shapeOf(text)?.takeIf { it.host.isNotEmpty() } ?: return null
        return resolvedPort(shape.portText, grpcPort)?.let { ParsedAddress(shape.host, shape.prefix, it) }
    }
}
