// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import java.io.File
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val PRIVILEGES = listOf("SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE")

/**
 * Rules "Функции в базе — только гарантии, и каждая записана в ADR" and "Права на новую таблицу выдаются сами" of
 * docs/specs/server/database-roles.feature: the database is compared with docs/adr/0069-database-roles.md, so a
 * function,
 * a trigger or a table with rights of its own that the ADR does not name (or the ADR names and the database lacks)
 * fails
 * the build.
 */
class DatabaseObjectsAdrTest {
    private lateinit var cluster: Cluster

    @BeforeTest
    fun `a prepared installation`() {
        cluster = Cluster()
        cluster.bootstrap()
        Preparation.migrate(cluster.url())
    }

    @AfterTest
    fun `stop the cluster`() = cluster.close()

    private val adr: List<String>
        get() {
            val file = File("../docs/adr/0069-database-roles.md")
            check(file.isFile) { "expected to run with the server module as the working directory: $file" }
            return file.readLines()
        }

    /**
     * The bullets of the ADR below the heading that starts with [heading], up to the next heading, as ``name`` to the
     * rest.
     */
    private fun bullets(heading: String): List<Pair<String, String>> {
        val lines = adr.dropWhile { !it.startsWith(heading) }.drop(1).takeWhile { !it.startsWith("#") }
        return lines.mapNotNull {
            Regex("""^- `([^`]+)`( — (.*))?""").find(it)?.let { m ->
                m.groupValues[1] to
                    m
                        .groupValues[3]
            }
        }
    }

    private fun strings(sql: String): List<String> =
        cluster.admin().use { c ->
            c.createStatement().use { s ->
                s.executeQuery(sql).use { rs -> generateSequence { if (rs.next()) rs.getString(1) else null }.toList() }
            }
        }

    @Test
    fun `Каждая функция и триггер схемы sard перечислены в ADR`() {
        val inAdr = bullets("### 11.").map { it.first }.sorted()
        val inDatabase =
            strings(
                """
                select p.proname || '(' || oidvectortypes(p.proargtypes) || ')' from pg_proc p
                where p.pronamespace = 'sard'::regnamespace order by 1
                """.trimIndent(),
            )
        val triggers =
            strings(
                """
                select t.tgname from pg_trigger t join pg_class c on c.oid = t.tgrelid
                where c.relnamespace = 'sard'::regnamespace and not t.tgisinternal
                """.trimIndent(),
            )

        assertTrue(inAdr.isNotEmpty(), "the ADR lists the functions")
        assertEquals(inAdr, inDatabase, "functions of the schema sard and the list of the ADR")
        assertEquals(emptyList(), triggers, "the ADR says: no triggers")
        assertTrue(adr.any { it.startsWith("Триггеры: нет") })
    }

    @Test
    fun `Таблица только-дописывания без явного отзыва UPDATE и DELETE роняет сборку`() {
        val listed =
            bullets("### 12.").associate { (table, rights) ->
                table to
                    rights
                        .removePrefix("`")
                        .removeSuffix("`")
                        .split(", ")
                        .toSet()
            }
        val tables =
            strings(
                "select relname from pg_class where relnamespace = 'sard'::regnamespace and relkind in ('r', 'p') " +
                    "and not relispartition",
            )

        assertTrue(listed.isNotEmpty() && listed.keys.all { it in tables }, "the tables of the ADR exist: $listed")
        for (table in tables) {
            val expected = listed[table] ?: (PRIVILEGES - "TRUNCATE").toSet()
            val actual =
                PRIVILEGES
                    .filter {
                        strings("select has_table_privilege('sard_app', 'sard.$table', '$it')::text").single() == "true"
                    }.toSet()
            assertEquals(expected, actual, "the rights of sard_app on $table")
        }
    }

    @Test
    fun `Перечень таблиц только-дописывания в ADR совпадает со списками bootstrap sql`() {
        val listed =
            bullets("### 12.").associate { (table, rights) ->
                table to
                    rights
                        .removePrefix("`")
                        .removeSuffix("`")
                        .split(", ")
                        .toSet()
            }
        val script = File("../deploy/db/bootstrap.sql").readText()

        fun array(name: String) =
            Regex("""$name constant text\[] := ARRAY\[([^]]*)]""")
                .find(script)!!
                .groupValues[1]
                .split(",")
                .map {
                    it.trim().trim('\'')
                }.filter { it.isNotEmpty() }
                .toSet()

        assertEquals(listed.filterValues { it == setOf("SELECT", "INSERT") }.keys, array("append_only"))
        assertEquals(listed.filterValues { it == setOf("SELECT") }.keys, array("read_only"))
    }
}
