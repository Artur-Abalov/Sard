// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals

private val POSTGRESQL = File("../agent/plugins/postgresql/schema.json").readText()
private val FILES = File("../agent/plugins/files/schema.json").readText()
private const val PG_CONFIG = """{"host":"db.example.com","database":"app","user":"backup","password_ref":"pg"}"""

private fun bound(
    vararg targets: String,
    plugin: String = "postgresql",
    problem: String = "",
) = """[{"name":"pg","plugin":"$plugin","targets":[${targets.joinToString(",") { "\"$it\"" }}],"problem":"$problem"}]"""

/**
 * S11: the console warns about a source the binding of its secret on the agent host does not allow; the grammar of
 * a target is the agent's (agent/internal/secretbind/pattern_test.go).
 */
@MutFlowTest
class SecretBindingsTest {
    private fun warnings(
        bindings: String,
        config: String = PG_CONFIG,
        schema: String = POSTGRESQL,
    ) = MutFlow.underTest { SecretBindings.warnings("postgresql", schema, config, bindings) }

    private fun target(
        host: String,
        port: Int = 5432,
        database: String = "app",
    ) = SecretTarget(host, port, database)

    @Test
    fun `a source at a target of the binding has no warning`() {
        assertEquals(emptyList(), warnings(bound("db.example.com:5432/app")))
        assertEquals(emptyList(), warnings(bound("other", "DB.example.com:*/*")))
    }

    @Test
    fun `a source at another target says which and what to run on the host`() {
        assertEquals(
            listOf(
                "secret \"pg\" is not allowed for postgresql at db.example.com:5432/app; if that is intended, " +
                    "run on the agent host: " +
                    "sudo sard-agent secret set pg --plugin postgresql --target db.example.com:5432/app",
            ),
            warnings(bound("db.example.com:5432/billing")),
        )
    }

    @Test
    fun `the port of the config, or the default of the schema, is the port of the target`() {
        val on6432 = PG_CONFIG.replace("}", ""","port":6432}""")
        assertEquals(emptyList(), warnings(bound("db.example.com:6432/app"), on6432))
        assertEquals(1, warnings(bound("db.example.com:6432/app")).size)
    }

    @Test
    fun `a secret bound to another plugin, or given to none, is a warning`() {
        assertEquals(
            listOf(
                "secret \"pg\" is bound to the plugin files, not postgresql; run on the agent host: " +
                    "sudo sard-agent secret set pg --plugin postgresql --target db.example.com:5432/app",
            ),
            warnings(bound("db.example.com", plugin = "files")),
        )
        assertEquals(
            listOf(
                "secret \"pg\" is given to no plugin on the agent host (no binding); run on the agent host: " +
                    "sudo sard-agent secret set pg --plugin postgresql --target db.example.com:5432/app",
            ),
            warnings(bound(plugin = "", problem = "no binding")),
        )
    }

    @Test
    fun `an agent that reported no binding of the secret, or a schema without a secret, gives no warning`() {
        assertEquals(emptyList(), warnings("[]"))
        assertEquals(emptyList(), warnings(bound("x"), """{"paths":["/etc"]}""", FILES))
        assertEquals(emptyList(), warnings("not json"))
        assertEquals(emptyList(), warnings(bound("x"), "not json"))
    }

    @Test
    fun `only the fields of a secret name secrets`() {
        val userPg = PG_CONFIG.replace("\"user\":\"backup\"", "\"user\":\"pg\"")
        assertEquals(1, warnings(bound("elsewhere"), userPg).size)
    }

    @Test
    fun `a schema without a target only checks the plugin`() {
        val schema = """{"type":"object","properties":{"token":{"type":"string","format":"sard-secret"}}}"""
        assertEquals(emptyList(), warnings(bound("anything"), """{"token":"pg"}""", schema))
        assertEquals(1, warnings(bound("anything", plugin = "files"), """{"token":"pg"}""", schema).size)
    }

    @Test
    fun `a target is matched as the agent matches it`() {
        val cases =
            mapOf(
                "db.example.com:5432/app" to
                    listOf(
                        target("db.example.com") to true,
                        target("DB.Example.COM") to true,
                        target("db.example.com", 5433) to false,
                        target("db.example.com", database = "App") to false,
                        target("db.example.com.evil") to false,
                    ),
                "db.example.com" to listOf(target("db.example.com", 1, "x") to true, target("other") to false),
                "db.example.com/app" to
                    listOf(target("db.example.com", 6432) to true, target("db.example.com", database = "x") to false),
                "db:*/app" to listOf(target("db", 6432) to true),
                "[::1]:5432/app" to
                    listOf(
                        target("::1") to true,
                        target("0:0::1") to true,
                        target("::2") to false,
                        target("localhost") to false,
                    ),
                "[::1]/app" to listOf(target("::1", 1) to true, target("::1", database = "x") to false),
                "::1" to listOf(target("0::1", 7) to true),
                "127.0.0.1" to listOf(target("127.0.0.1") to true, target("localhost") to false),
                "localhost" to listOf(target("127.0.0.1") to false, target("LocalHost") to true),
                "/var/run/postgresql:5432/app" to
                    listOf(
                        target("/var/run/postgresql") to true,
                        target("/var/run/postgresql/") to true,
                        target("/var/run/postgresql", 5433) to false,
                        target("/tmp") to false,
                    ),
                "/var/run/postgresql" to
                    listOf(target("/var/run/postgresql/.", 5433, "x") to true, target("var/run/postgresql") to false),
                "/run/a:b:5432/app" to listOf(target("/run/a:b") to true),
                "[::1" to listOf(target("::1") to false),
            )
        for ((pattern, targets) in cases) {
            for ((t, want) in targets) {
                assertEquals(want, MutFlow.underTest { SecretTargets.matches(pattern, t) }, "$pattern ~ $t")
            }
        }
    }

    @Test
    fun `a target is written as the operator binds it`() {
        val written = { t: SecretTarget -> MutFlow.underTest { t.toString() } }
        assertEquals("db:5432/app", written(target("db")))
        assertEquals("[::1]:5432/app", written(target("::1")))
        assertEquals("/var/run/postgresql:5432", written(target("/var/run/postgresql", database = "")))
        assertEquals("token", written(SecretTarget("token", 0, "")))
        assertEquals("::1/app", written(SecretTarget("::1", 0, "app")))
    }
}
