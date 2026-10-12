// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfbackup

/** The outcome of building the command that adds a repository to the built-in agent. */
sealed interface CommandBuild {
    /** [commands] are the lines to run in a terminal on the server's machine, in order. */
    data class Built(
        val address: String,
        val commands: List<String>,
    ) : CommandBuild

    /** The parameters at [fields] are missing or not acceptable. */
    data class Rejected(
        val fields: List<String>,
    ) : CommandBuild
}

private const val SELF_STORAGE_DIR = "/var/lib/sard-self"
private const val KEY_ID_PROMPT = "read -r -p \"S3 access key ID: \" SARD_S3_KEY_ID"
private const val KEY_ID_FLAG = "--access-key-id \"\$SARD_S3_KEY_ID\""
private const val RESTART = "docker compose restart self-agent"

/** One-off container of the built-in agent's image: root, with the five capabilities `repo add` needs for files. */
private const val RUN =
    "docker compose run --rm --no-deps -u 0 --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER " +
        "--cap-add SETUID --cap-add SETGID self-agent repo add"
private const val MAX_PATH = 512
private const val MAX_PORT = 65535

private val BUCKET = Regex("[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]")
private val REGION = Regex("[a-z0-9-]{1,64}")
private val USER = Regex("[a-z_][a-z0-9_-]{0,31}")
private val SEGMENT = Regex("[A-Za-z0-9._-]+")
private val FINGERPRINT = Regex("SHA256:[A-Za-z0-9+/]{43}")
private val PORT = Regex("[1-9][0-9]{0,4}")
private const val LABEL = "[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?"
private val DNS_NAME = Regex("(?=.{1,253}$)$LABEL(\\.$LABEL)*")
private val IPV6_LITERAL = Regex("\\[[0-9A-Fa-f:.]+]")
private val ENDPOINT = Regex("https?://([^:/@?#\\s]+|\\[[0-9A-Fa-f:.]+])(:[0-9]+)?")

private fun validHost(host: String) = DNS_NAME.matches(host) || IPV6_LITERAL.matches(host)

private fun validPort(port: String) = PORT.matches(port) && port.toInt() <= MAX_PORT

private fun plainSegment(segment: String) = SEGMENT.matches(segment) && segment != "." && segment != ".."

private fun validSegments(path: String) = path.length <= MAX_PATH && path.split('/').all(::plainSegment)

private fun validAbsolute(path: String) = path.startsWith("/") && validSegments(path.drop(1))

private fun validEndpoint(endpoint: String): Boolean {
    val match = ENDPOINT.matchEntire(endpoint) ?: return false
    val port = match.groupValues[2].drop(1)
    return validHost(match.groupValues[1]) && (port.isEmpty() || validPort(port))
}

/** The parameters as the request carried them; what is empty is absent. Collects the fields that do not fit. */
private class Fields(
    private val values: Map<String, String>,
) {
    val invalid = mutableListOf<String>()

    fun required(
        name: String,
        valid: (String) -> Boolean,
    ): String? = values[name].takeIf { it != null && valid(it) }.also { if (it == null) invalid += name }

    fun optional(
        name: String,
        valid: (String) -> Boolean,
    ): String? {
        val value = values[name] ?: return null
        return value.takeIf(valid).also { if (it == null) invalid += name }
    }
}

/** What differs between storages: the address, the flags for `repo add` and whether it needs the key id asked. */
private class Storage(
    val address: String,
    val flags: List<String> = emptyList(),
    val asksKeyId: Boolean = false,
)

private fun s3(fields: Fields): Storage? {
    val provider = fields.required("provider") { it in setOf("aws", "b2", "custom") }
    val region = if (provider == "custom") null else fields.required("region", REGION::matches)
    val endpoint = if (provider == "custom") fields.required("endpoint", ::validEndpoint) else null
    val bucket = fields.required("bucket", BUCKET::matches)
    val path = fields.optional("path", ::validSegments)
    if (fields.invalid.isNotEmpty() || provider == null || bucket == null) return null
    val location = bucket + path?.let { "/$it" }.orEmpty()
    return if (endpoint != null) {
        Storage("s3:$endpoint/$location", listOf(KEY_ID_FLAG), asksKeyId = true)
    } else {
        Storage("s3:$location", listOf("--provider $provider", "--region $region", KEY_ID_FLAG), asksKeyId = true)
    }
}

private fun sftp(fields: Fields): Storage? {
    val user = fields.required("user", USER::matches)
    val host = fields.required("host", ::validHost)
    val port = fields.optional("port", ::validPort)?.toInt()
    val path = fields.required("path", ::validAbsolute)
    val fingerprint = fields.optional("hostKeyFingerprint", FINGERPRINT::matches)
    if (fields.invalid.isNotEmpty()) return null
    val address = if (port == null) "sftp:$user@$host:$path" else "sftp://$user@$host:$port/$path"
    // The fingerprint is checked above to hold nothing the shell reads: it goes in as it is.
    return Storage(address, listOfNotNull(fingerprint?.let { "--host-key-fingerprint $it" }))
}

/**
 * The commands that add a repository to the built-in agent (F4b, Р23–Р25, Р29): built by the server from
 * parameters that are not secrets, run by the owner on the server's machine. No secret goes through the server: the
 * key id is asked in the terminal and the secret key by `repo add` itself.
 */
object RepositoryCommands {
    /** The first of self, self-2, self-3, ... that [taken] does not hold. */
    fun freeName(taken: Collection<String>): String =
        generateSequence(1) { it + 1 }.map { if (it == 1) "self" else "self-$it" }.first { it !in taken }

    fun build(
        name: String,
        params: Map<String, String>,
    ): CommandBuild {
        val fields = Fields(params.filterValues { it.isNotEmpty() })
        val storage =
            when (fields.required("storage") { it in setOf("s3", "sftp", "local") }) {
                "s3" -> s3(fields)
                "sftp" -> sftp(fields)
                "local" -> Storage("$SELF_STORAGE_DIR/$name")
                else -> null
            }
        if (storage == null) return CommandBuild.Rejected(fields.invalid)
        return CommandBuild.Built(storage.address, commands(name, storage))
    }

    private fun commands(
        name: String,
        storage: Storage,
    ): List<String> {
        val words = storage.flags + shellWord(name) + shellWord(storage.address)
        val add = "$RUN ${words.joinToString(" ")} --no-restart --config $SELF_AGENT_CONFIG"
        return listOfNotNull(KEY_ID_PROMPT.takeIf { storage.asksKeyId }, add, RESTART)
    }
}
