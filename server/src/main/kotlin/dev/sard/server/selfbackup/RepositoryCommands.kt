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

private val PROVIDERS = setOf("aws", "b2", "custom")

/** The bucket and, when there is one, the folder in it as the address holds them; null without a bucket. */
private fun locationOf(fields: Fields): String? {
    val bucket = fields.required("bucket", BUCKET::matches)
    val path = fields.optional("path", ::validSegments)
    return bucket?.let { it + path?.let { folder -> "/$folder" }.orEmpty() }
}

/** AWS or Backblaze B2: the address is the bucket, the rest is built from the region by `repo add`. */
private fun hostedS3(
    fields: Fields,
    provider: String?,
): Storage? {
    val region = fields.required("region", REGION::matches)
    val location = locationOf(fields).takeIf { fields.invalid.isEmpty() }
    // The location is null when anything is invalid; the checks let the compiler prove what the address holds.
    if (provider == null || region == null || location == null) return null
    return Storage("s3:$location", listOf("--provider $provider", "--region $region", KEY_ID_FLAG), asksKeyId = true)
}

/** An S3 of the owner's own: the address names the endpoint. */
private fun customS3(fields: Fields): Storage? {
    val endpoint = fields.required("endpoint", ::validEndpoint)
    val location = locationOf(fields).takeIf { fields.invalid.isEmpty() }
    if (endpoint == null || location == null) return null
    return Storage("s3:$endpoint/$location", listOf(KEY_ID_FLAG), asksKeyId = true)
}

private fun s3(fields: Fields): Storage? {
    val provider = fields.required("provider") { it in PROVIDERS }
    return if (provider == "custom") customS3(fields) else hostedS3(fields, provider)
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

private fun planOf(
    storage: String?,
    name: String,
    fields: Fields,
): Storage? =
    when (storage) {
        "s3" -> s3(fields)
        "sftp" -> sftp(fields)
        "local" -> Storage("$SELF_STORAGE_DIR/$name")
        else -> null
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
        val storage = fields.required("storage") { it in setOf("s3", "sftp", "local") }
        // A storage whose field did not fit has no plan: it refuses the whole request.
        val plan = planOf(storage, name, fields) ?: return CommandBuild.Rejected(fields.invalid)
        return CommandBuild.Built(plan.address, commands(name, plan))
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
