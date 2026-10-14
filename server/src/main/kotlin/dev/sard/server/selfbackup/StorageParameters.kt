// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

// What a parameter of the storage may hold (F4b, Р29). The values go into a command the owner runs in a terminal, so
// each one is checked against the characters it may have, and a value outside them is refused with the field named.

private const val MAX_PATH = 512
private const val MAX_PORT = 65535
private const val LABEL = "[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?"

internal val BUCKET = Regex("[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]")
internal val REGION = Regex("[a-z0-9-]{1,64}")
internal val USER = Regex("[a-z_][a-z0-9_-]{0,31}")
internal val FINGERPRINT = Regex("SHA256:[A-Za-z0-9+/]{43}")
private val SEGMENT = Regex("[A-Za-z0-9._-]+")
private val PORT = Regex("[1-9][0-9]{0,4}")
private val DNS_NAME = Regex("(?=.{1,253}$)$LABEL(\\.$LABEL)*")
private val IPV6_LITERAL = Regex("\\[[0-9A-Fa-f:.]+]")
private val ENDPOINT = Regex("https?://([^:/@?#\\s]+|\\[[0-9A-Fa-f:.]+])(:[0-9]+)?")

internal fun validHost(host: String) = DNS_NAME.matches(host) || IPV6_LITERAL.matches(host)

internal fun validPort(port: String) = PORT.matches(port) && port.toInt() <= MAX_PORT

internal fun plainSegment(segment: String) = SEGMENT.matches(segment) && segment != "." && segment != ".."

internal fun validSegments(path: String) = path.length <= MAX_PATH && path.split('/').all(::plainSegment)

internal fun validAbsolute(path: String) = path.startsWith("/") && validSegments(path.drop(1))

internal fun validEndpoint(endpoint: String): Boolean {
    val match = ENDPOINT.matchEntire(endpoint) ?: return false
    val port = match.groupValues[2].drop(1)
    return validHost(match.groupValues[1]) && (port.isEmpty() || validPort(port))
}
