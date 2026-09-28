// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.registration

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

private typealias Reason = RegistrationRejectedException.Reason

private val HEX64 = "0123456789abcdef".repeat(4)

private fun plugin(
    name: String = "postgresql",
    version: String = "0.1.0",
    configSchema: String = """{"type":"object"}""",
    actions: List<PluginAction?> = listOf(PluginAction.BACKUP, PluginAction.RESTORE, PluginAction.VERIFY),
) = PluginEntry(name, version, configSchema, actions)

private fun repository(
    name: String = "main",
    backend: String = "s3",
    repositoryId: String = HEX64,
    cryptoProvider: String = "",
) = RepositoryEntry(name, backend, repositoryId, cryptoProvider)

private fun snapshot(
    protocolVersion: Long = 1,
    hostname: String = "db1",
    agentVersion: String = "0.1.0",
    os: String = "linux",
    arch: String = "amd64",
    plugins: List<PluginEntry> = listOf(plugin()),
    repositories: List<RepositoryEntry> = listOf(repository()),
    secretNames: List<String> = listOf("pg-prod"),
    scriptNames: List<String> = listOf("pre-dump"),
) = AgentSnapshot(hostname, agentVersion, protocolVersion, os, arch, plugins, repositories, secretNames, scriptNames)

/** What a refusal says on the wire: the reason and the ErrorInfo metadata. */
private data class Refusal(
    val reason: Reason,
    val details: Map<String, String>,
)

private fun invalid(field: String) = Refusal(Reason.FIELD_INVALID, mapOf("field" to field))

private fun tooLarge(
    field: String,
    limit: Int,
) = Refusal(Reason.SNAPSHOT_TOO_LARGE, mapOf("field" to field, "limit" to "$limit"))

/** S4a: limits on what an authenticated, possibly compromised host may announce. */
@MutFlowTest
class SnapshotRulesTest {
    private fun refusalOf(candidate: AgentSnapshot): Refusal? =
        try {
            MutFlow.underTest { SnapshotRules.check(candidate) }
            null
        } catch (e: RegistrationRejectedException) {
            Refusal(e.reason, e.details)
        }

    private fun names(
        count: Int,
        prefix: String = "n",
    ) = List(count) { "$prefix$it" }

    // --- accepted

    @Test
    fun `a snapshot like the one the agent sends is accepted`() {
        assertNull(refusalOf(snapshot()))
    }

    @Test
    fun `an empty snapshot is accepted`() {
        val empty =
            snapshot(
                plugins = emptyList(),
                repositories = emptyList(),
                secretNames = emptyList(),
                scriptNames = emptyList(),
            )
        assertNull(refusalOf(empty))
    }

    @Test
    fun `every limit reached exactly is accepted`() {
        val atLimits =
            snapshot(
                hostname = "h".repeat(253),
                agentVersion = "v".repeat(64),
                plugins = names(SnapshotRules.MAX_PLUGINS, "p").map { plugin(name = it) },
                repositories = names(SnapshotRules.MAX_REPOSITORIES, "r").map { repository(name = it) },
                secretNames = names(SnapshotRules.MAX_SECRET_NAMES),
                scriptNames = names(SnapshotRules.MAX_SCRIPT_NAMES),
            )
        assertNull(refusalOf(atLimits))
    }

    @Test
    fun `a name of 128 characters from the whole alphabet is accepted`() {
        val name = "Aa0._-" + "z".repeat(122)
        assertNull(refusalOf(snapshot(secretNames = listOf(name, "PG_PASSWORD"))))
    }

    @Test
    fun `a config schema of exactly 64 KiB is accepted`() {
        val schema = "\"" + "x".repeat(SnapshotRules.MAX_CONFIG_SCHEMA_BYTES - 2) + "\""
        assertNull(refusalOf(snapshot(plugins = listOf(plugin(configSchema = schema)))))
    }

    @Test
    fun `an empty repository id and crypto provider mean unknown and built-in`() {
        assertNull(refusalOf(snapshot(repositories = listOf(repository(repositoryId = "", cryptoProvider = "")))))
    }

    @Test
    fun `a named crypto provider and any restic backend prefix are accepted`() {
        val rest = repository(name = "a", backend = "rest", cryptoProvider = "gost")
        val repos = listOf(rest, repository(name = "b", backend = "b2"))
        assertNull(refusalOf(snapshot(repositories = repos)))
    }

    // --- protocol

    @Test
    fun `protocol 0 is PROTOCOL_UNSUPPORTED with the supported range`() {
        val expected = Refusal(Reason.PROTOCOL_UNSUPPORTED, mapOf("min_supported" to "1", "max_supported" to "1"))
        assertEquals(expected, refusalOf(snapshot(protocolVersion = 0)))
    }

    @Test
    fun `protocol 2 is PROTOCOL_UNSUPPORTED`() {
        assertEquals(Reason.PROTOCOL_UNSUPPORTED, refusalOf(snapshot(protocolVersion = 2))?.reason)
    }

    @Test
    fun `protocol is checked before anything else`() {
        assertEquals(Reason.PROTOCOL_UNSUPPORTED, refusalOf(snapshot(protocolVersion = 0, hostname = ""))?.reason)
    }

    // --- scalar fields

    @Test
    fun `an empty or overlong hostname is HOSTNAME_INVALID, as in Enroll`() {
        val expected = Refusal(Reason.HOSTNAME_INVALID, mapOf("field" to "hostname"))
        assertEquals(expected, refusalOf(snapshot(hostname = "")))
        assertEquals(expected, refusalOf(snapshot(hostname = "h".repeat(254))))
        assertEquals(expected, refusalOf(snapshot(hostname = "db1\u0000")))
    }

    @Test
    fun `agent version, os and arch must be 1 to 64 printable ASCII characters without spaces`() {
        assertEquals(invalid("agent_version"), refusalOf(snapshot(agentVersion = "")))
        assertEquals(invalid("agent_version"), refusalOf(snapshot(agentVersion = "v".repeat(65))))
        assertEquals(invalid("os"), refusalOf(snapshot(os = "li nux")))
        assertEquals(invalid("arch"), refusalOf(snapshot(arch = "amd64\u0000")))
        assertEquals(invalid("arch"), refusalOf(snapshot(arch = "ämd64")))
    }

    // --- plugins

    @Test
    fun `more than 64 plugins is SNAPSHOT_TOO_LARGE`() {
        val plugins = names(SnapshotRules.MAX_PLUGINS + 1, "p").map { plugin(name = it) }
        assertEquals(tooLarge("plugins", 64), refusalOf(snapshot(plugins = plugins)))
    }

    @Test
    fun `a plugin name outside the name format is NAME_INVALID`() {
        val expected = Refusal(Reason.NAME_INVALID, mapOf("field" to "plugins[1].name"))
        for (bad in listOf("", "-pg", ".pg", "pg sql", "pg/sql", "p".repeat(129))) {
            val plugins = listOf(plugin(name = "ok"), plugin(name = bad))
            assertEquals(expected, refusalOf(snapshot(plugins = plugins)), bad)
        }
    }

    @Test
    fun `a repeated plugin name is NAME_DUPLICATE`() {
        val plugins = listOf(plugin(name = "pg"), plugin(name = "files"), plugin(name = "pg"))
        val expected = Refusal(Reason.NAME_DUPLICATE, mapOf("field" to "plugins[2].name"))
        assertEquals(expected, refusalOf(snapshot(plugins = plugins)))
    }

    @Test
    fun `a plugin version outside the label format is FIELD_INVALID`() {
        assertEquals(invalid("plugins[0].version"), refusalOf(snapshot(plugins = listOf(plugin(version = "")))))
    }

    @Test
    fun `a config schema over 64 KiB in UTF-8 is SNAPSHOT_TOO_LARGE`() {
        // 32768 two-byte characters plus the quotes: fewer than 64 Ki chars, more than 64 KiB.
        val schema = "\"" + "я".repeat(SnapshotRules.MAX_CONFIG_SCHEMA_BYTES / 2) + "\""
        val expected = tooLarge("plugins[0].config_schema", SnapshotRules.MAX_CONFIG_SCHEMA_BYTES)
        assertEquals(expected, refusalOf(snapshot(plugins = listOf(plugin(configSchema = schema)))))
    }

    @Test
    fun `a config schema that is not one JSON value is CONFIG_SCHEMA_INVALID`() {
        val expected = Refusal(Reason.CONFIG_SCHEMA_INVALID, mapOf("field" to "plugins[0].config_schema"))
        for (bad in listOf("", "   ", "{", "{\"a\":}", "{} {}", "{} x", "nul")) {
            assertEquals(expected, refusalOf(snapshot(plugins = listOf(plugin(configSchema = bad)))), bad)
        }
    }

    @Test
    fun `a config schema PostgreSQL cannot store, with a NUL character, is CONFIG_SCHEMA_INVALID`() {
        val schema = """{"description":"a\u0000b"}"""
        val refusal = refusalOf(snapshot(plugins = listOf(plugin(configSchema = schema))))
        assertEquals(Reason.CONFIG_SCHEMA_INVALID, refusal?.reason)
    }

    @Test
    fun `any JSON value is syntactically a schema, booleans included`() {
        assertNull(refusalOf(snapshot(plugins = listOf(plugin(configSchema = "true")))))
    }

    @Test
    fun `an unknown or repeated action is FIELD_INVALID`() {
        val unknown = plugin(actions = listOf(PluginAction.BACKUP, null))
        assertEquals(invalid("plugins[0].actions"), refusalOf(snapshot(plugins = listOf(unknown))))
        val repeated = plugin(actions = listOf(PluginAction.RUN, PluginAction.RUN))
        assertEquals(invalid("plugins[0].actions"), refusalOf(snapshot(plugins = listOf(repeated))))
    }

    // --- repositories

    @Test
    fun `more than 256 repositories is SNAPSHOT_TOO_LARGE`() {
        val repos = names(SnapshotRules.MAX_REPOSITORIES + 1, "r").map { repository(name = it) }
        assertEquals(tooLarge("repositories", 256), refusalOf(snapshot(repositories = repos)))
    }

    @Test
    fun `a bad or repeated repository name is NAME_INVALID or NAME_DUPLICATE`() {
        val bad = listOf(repository(name = "main"), repository(name = "a b"))
        val invalidName = Refusal(Reason.NAME_INVALID, mapOf("field" to "repositories[1].name"))
        assertEquals(invalidName, refusalOf(snapshot(repositories = bad)))
        val repeated = listOf(repository(name = "main"), repository(name = "main"))
        val expected = Refusal(Reason.NAME_DUPLICATE, mapOf("field" to "repositories[1].name"))
        assertEquals(expected, refusalOf(snapshot(repositories = repeated)))
    }

    @Test
    fun `a backend that is not a lower-case scheme of up to 16 characters is FIELD_INVALID`() {
        for (bad in listOf("", "S3", "s3://", "1s", "s".repeat(17))) {
            val repos = listOf(repository(backend = bad))
            assertEquals(invalid("repositories[0].backend"), refusalOf(snapshot(repositories = repos)), bad)
        }
    }

    @Test
    fun `a repository id that is not 64 lower-case hex digits is FIELD_INVALID`() {
        for (bad in listOf(HEX64.drop(1), HEX64.uppercase(), HEX64 + "0", "g" + HEX64.drop(1))) {
            val repos = listOf(repository(repositoryId = bad))
            assertEquals(invalid("repositories[0].repository_id"), refusalOf(snapshot(repositories = repos)), bad)
        }
    }

    @Test
    fun `a crypto provider outside the name format is FIELD_INVALID`() {
        val repos = listOf(repository(cryptoProvider = "gost 2012"))
        assertEquals(invalid("repositories[0].crypto_provider"), refusalOf(snapshot(repositories = repos)))
    }

    // --- secret and script names

    @Test
    fun `more than 1024 secret or script names is SNAPSHOT_TOO_LARGE`() {
        val secrets = snapshot(secretNames = names(SnapshotRules.MAX_SECRET_NAMES + 1))
        assertEquals(tooLarge("secret_names", 1024), refusalOf(secrets))
        val scripts = snapshot(scriptNames = names(SnapshotRules.MAX_SCRIPT_NAMES + 1))
        assertEquals(tooLarge("script_names", 1024), refusalOf(scripts))
    }

    @Test
    fun `a bad secret or script name is NAME_INVALID`() {
        val secrets = snapshot(secretNames = listOf("ok", "pg prod"))
        assertEquals(Refusal(Reason.NAME_INVALID, mapOf("field" to "secret_names[1]")), refusalOf(secrets))
        val scripts = snapshot(scriptNames = listOf("../etc/passwd"))
        assertEquals(Refusal(Reason.NAME_INVALID, mapOf("field" to "script_names[0]")), refusalOf(scripts))
    }

    @Test
    fun `a repeated secret or script name is NAME_DUPLICATE`() {
        val secrets = snapshot(secretNames = listOf("a", "a"))
        assertEquals(Refusal(Reason.NAME_DUPLICATE, mapOf("field" to "secret_names[1]")), refusalOf(secrets))
        val scripts = snapshot(scriptNames = listOf("x", "y", "x"))
        assertEquals(Refusal(Reason.NAME_DUPLICATE, mapOf("field" to "script_names[2]")), refusalOf(scripts))
    }
}
