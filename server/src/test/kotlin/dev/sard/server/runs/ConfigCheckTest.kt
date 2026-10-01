// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val FILES = File("../agent/plugins/files/schema.json").readText()
private const val SECRET_SCHEMA =
    """{"type":"object","properties":{"password":{"type":"string","format":"sard-secret"}},"required":["password"]}"""

/** A source's config is checked against the plugin's schema, JSON Schema 2020-12 (S8b В15, ADR 0027). */
@MutFlowTest
class ConfigCheckTest {
    private fun fields(
        schema: String,
        config: String,
        secrets: Set<String> = emptySet(),
    ) = MutFlow.underTest { ConfigCheck.violations(schema, config, secrets) }.map { it.field }

    @Test
    fun `a config that fits its schema has no violations`() {
        assertEquals(emptyList(), fields(FILES, """{"paths":["/etc"],"exclude":["*.log"],"one_file_system":true}"""))
    }

    @Test
    fun `every violation is named by the path of its field, in path order`() {
        assertEquals(
            listOf("config/one_file_system", "config/paths/0"),
            fields(FILES, """{"paths":["etc"],"one_file_system":"yes"}"""),
        )
    }

    @Test
    fun `a missing required field and an unknown field are named by the object itself`() {
        assertEquals(listOf("config"), fields(FILES, "{}"))
        assertEquals(listOf("config"), fields(FILES, """{"paths":["/etc"],"compression":"max"}"""))
    }

    @Test
    fun `fields the schema does not know, like the translations, change nothing`() {
        assertTrue(FILES.contains("x-sard-i18n"))
        assertEquals(emptyList(), fields(FILES, """{"paths":["/etc"]}"""))
    }

    @Test
    fun `a secret field must name a secret of the agent`() {
        assertEquals(emptyList(), fields(SECRET_SCHEMA, """{"password":"db-password"}""", setOf("db-password")))
        val violations = ConfigCheck.violations(SECRET_SCHEMA, """{"password":"other"}""", setOf("db-password"))
        assertEquals(listOf("config/password"), violations.map { it.field })
        assertTrue("\"other\"" in violations.single().message, violations.single().message)
    }

    @Test
    fun `a schema that is JSON but not a JSON Schema refuses every config at config`() {
        assertEquals(listOf("config"), fields("""{"type": 5}""", "{}"))
        assertEquals(listOf("config"), fields("""[1]""", "{}"))
    }

    @Test
    fun `the message names the rule and never repeats a value of the config`() {
        val message = ConfigCheck.violations(FILES, """{"paths":["/etc"],"x-secret-key":"hunter2"}""", emptySet()).single().message
        assertTrue("hunter2" !in message, message)
    }

    /** A schema comes from the agent host, which may be compromised: the server never fetches what it points to. */
    @Test
    fun `a schema that refers outside itself is refused, nothing is fetched`() {
        for (reference in listOf("file:///etc/hostname", "http://127.0.0.1:1/schema.json", "https://example.invalid/x.json")) {
            val violations = ConfigCheck.violations("""{"${'$'}ref":"$reference"}""", "{}", emptySet())

            assertEquals(listOf("config"), violations.map { it.field }, reference)
            assertEquals("the plugin's config schema is not a valid JSON Schema", violations.single().message, reference)
        }
    }

    @Test
    fun `a reference inside the schema is followed`() {
        val schema =
            """
            {"${'$'}defs":{"path":{"type":"string","pattern":"^/"}},"type":"object",
             "properties":{"p":{"${'$'}ref":"#/${'$'}defs/path"}}}
            """.trimIndent()

        assertEquals(listOf("config/p"), fields(schema, """{"p":"etc"}"""))
    }
}
