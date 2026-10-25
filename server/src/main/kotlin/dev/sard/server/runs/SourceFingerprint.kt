// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import tools.jackson.core.JacksonException
import tools.jackson.databind.JsonNode
import tools.jackson.databind.json.JsonMapper
import java.security.MessageDigest

private val JSON = JsonMapper.builder().build()
private val HEX64 = Regex("^[0-9a-f]{64}$")

/**
 * The fingerprint of a source as a backup brought it (V2, proto BackupOutput.fingerprint): the canonical bytes
 * the agent wrote and their SHA-256, the value of the snapshot's tag sard.fp. The server does not canonicalise:
 * it hashes the bytes as they came, as the verifier does (ADR 00XX, source fingerprint).
 */
class SourceFingerprint(
    val bytes: ByteArray,
    val sha256: String,
) {
    /** Why this fingerprint cannot be kept, or null when it can. */
    fun problem(): String? =
        when {
            bytes.isEmpty() || sha256.isEmpty() -> "a fingerprint without its hash or a hash without its fingerprint"
            bytes.size > MAX_BYTES -> "a fingerprint larger than 1 MiB"
            !HEX64.matches(sha256) -> "a fingerprint hash that is not 64 lower-case hex digits"
            hex(bytes) != sha256 -> "a fingerprint whose hash is not that of its bytes"
            parsed() == null -> "a fingerprint that is not one"
            else -> null
        }

    /** What the console shows of it: every fact but those of the hashed sample, which name files and their hashes. */
    fun summary(): FingerprintSummary {
        val node = checkNotNull(parsed()) { "a fingerprint without a problem parses" }
        val facts = factsOf(node)
        val (hidden, shown) = facts.partition { fact -> HIDDEN.any { fact.name.startsWith(it) } }
        return FingerprintSummary(sha256, node.path("format").asInt(), node.text("plugin"), shown, hidden.size)
    }

    private fun parsed(): JsonNode? =
        try {
            JSON.readTree(bytes).takeIf { it.isObject && it.path("facts").isArray }
        } catch (_: JacksonException) {
            null
        }

    override fun equals(other: Any?): Boolean =
        other is SourceFingerprint && sha256 == other.sha256 && bytes.contentEquals(other.bytes)

    override fun hashCode(): Int = sha256.hashCode()

    override fun toString() = "SourceFingerprint(sha256=$sha256)"

    companion object {
        const val MAX_BYTES = 1 shl 20

        /** Facts of the hashed sample of files: a path and the hash of its content, or its size and time. */
        private val HIDDEN = listOf("sha256:", "stat:")

        /** The fingerprint a backup sent; null when it sent none (an older agent, a plugin that takes none). */
        fun of(
            bytes: ByteArray,
            sha256: String,
        ): SourceFingerprint? = if (bytes.isEmpty() && sha256.isEmpty()) null else SourceFingerprint(bytes, sha256)

        private fun hex(bytes: ByteArray): String =
            MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it) }
    }
}

/** One fact of a fingerprint: rule "exact" is compared with the copy, "info" only shown. */
data class FingerprintFact(
    val name: String,
    val rule: String,
    val value: String,
)

/** The fingerprint of a backup as the console shows it; [hidden] facts of the hashed sample are only counted. */
data class FingerprintSummary(
    val sha256: String,
    val format: Int,
    val plugin: String,
    val facts: List<FingerprintFact>,
    val hidden: Int,
) {
    /** The JSON of the summary in run_steps.output. */
    fun fields(): Map<String, Any?> =
        mapOf(
            "sha256" to sha256,
            "format" to format,
            "plugin" to plugin,
            "facts" to facts.map { mapOf("name" to it.name, "rule" to it.rule, "value" to it.value) },
            "hidden" to hidden,
        )

    companion object {
        /** The summary stored by [fields]. */
        fun of(node: JsonNode): FingerprintSummary =
            FingerprintSummary(
                node.text("sha256"),
                node.path("format").asInt(),
                node.text("plugin"),
                factsOf(node),
                node.path("hidden").asInt(),
            )
    }
}

private fun JsonNode.text(name: String): String = path(name).asString()

/** The facts of a fingerprint or of a summary, in their order. */
private fun factsOf(node: JsonNode): List<FingerprintFact> {
    val facts = node.path("facts")
    return (0 until facts.size()).map { facts.get(it) }.map { FingerprintFact(it.text("name"), it.text("rule"), it.text("value")) }
}
