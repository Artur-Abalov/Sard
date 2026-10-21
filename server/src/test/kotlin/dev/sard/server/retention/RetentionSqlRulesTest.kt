// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import java.io.File
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

private val SQL_COMMENT = Regex("""--.*$""", RegexOption.MULTILINE)
private val STATEMENT_OWNER = Regex("""^\s*(?:CREATE|ALTER) TABLE (\w+)""", RegexOption.IGNORE_CASE)
private val TENANT_FOREIGN_KEY = Regex("""FOREIGN KEY \(tenant_id, (\w+)\)\s+REFERENCES (\w+)""", RegexOption.IGNORE_CASE)

/** The tables the cleaning deletes from: a foreign key into any of them must be dealt with by a batch. */
private val CLEANED =
    setOf(
        "runs",
        "run_steps",
        "schedule_fires",
        "restore_verifications",
        "verification_jobs",
        "notification_deliveries",
        "verification_stale_episodes",
    )

private val BATCHES =
    mapOf(
        "DELIVERIES" to DELIVERIES,
        "DOWNTIME_FIRES" to DOWNTIME_FIRES,
        "OTHER_FIRES" to OTHER_FIRES,
        "VERIFICATIONS" to VERIFICATIONS,
        "RUNS" to RUNS,
    )

/** One foreign key of the schema: [owner].[column] refers to [target]. */
private data class ForeignKey(
    val owner: String,
    val column: String,
    val target: String,
)

/**
 * Guards of the batches of RetentionSql.kt: a table that gets a foreign key into a cleaned table must come up in a
 * batch, or the cleaning would fail on the first row that is referred to (no ON DELETE anywhere), and every batch stays
 * inside one tenant (ADR 0013, rule 8).
 */
class RetentionSqlRulesTest {
    private fun foreignKeys(): List<ForeignKey> {
        val dir = File("src/main/resources/db/migration")
        check(dir.isDirectory) { "expected the Flyway migrations at $dir" }
        return dir
            .listFiles { f -> f.extension == "sql" }!!
            .sortedBy { it.name }
            .flatMap { it.readText().replace(SQL_COMMENT, "").split(';') }
            .flatMap { statement ->
                val owner = STATEMENT_OWNER.find(statement)?.groupValues?.get(1) ?: return@flatMap emptyList()
                TENANT_FOREIGN_KEY.findAll(statement).map { ForeignKey(owner, it.groupValues[1], it.groupValues[2]) }.toList()
            }
    }

    private fun names(
        text: String,
        word: String,
    ) = Regex("""\b$word\b""").containsMatchIn(text)

    /**
     * References that the cleaning does not meet in a batch: the rows of `snapshots` are never deleted and a run
     * with one is not picked at all, and `step_logs` lines leave with their partition; the batches name both in
     * their NOT EXISTS (checked below), so they need no entry here. Listed only when a batch cannot name the key.
     */
    private val unnamedOnPurpose = emptySet<ForeignKey>()

    @Test
    fun `Каждая внешняя ссылка на очищаемую таблицу названа в пачке уборки`() {
        val keys = foreignKeys()
        assertTrue(ForeignKey("snapshots", "step_id", "run_steps") in keys, "sanity: snapshots.step_id was not found")
        assertTrue(ForeignKey("step_logs", "step_id", "run_steps") in keys, "sanity: step_logs.step_id was not found")

        val missing =
            keys
                .filter { it.target in CLEANED && it !in unnamedOnPurpose }
                .filter { key -> BATCHES.values.none { names(it, key.owner) && names(it, key.column) } }

        assertTrue(missing.isEmpty(), "foreign keys into cleaned tables that no batch names:\n${missing.joinToString("\n")}")
    }

    @Test
    fun `Каждая пачка выбирает и удаляет внутри одного тенанта`() {
        for ((name, sql) in BATCHES) {
            val deletes = Regex("""\bdelete from\b""").findAll(sql).count()
            val scopedDeletes =
                Regex(
                    """delete from \w+(?:\s+\w+)?(?:\s+using\s+\w+\s+\w+)?\s+where\s+(?:\w+\.)?tenant_id\s*=\s*:tenant""",
                ).findAll(sql).count()
            assertEquals(deletes, scopedDeletes, "$name: a delete without tenant_id = :tenant")

            for (lock in Regex("""for update of""").findAll(sql)) {
                val before = sql.substring(0, lock.range.first)
                val start = maxOf(before.lastIndexOf("picked as ("), before.lastIndexOf("id in ("))
                assertTrue("tenant_id = :tenant" in before.substring(start), "$name: a pick without tenant_id = :tenant")
            }
        }
    }

    @Test
    fun `Охрана находит пропущенную ссылку`() {
        val key = ForeignKey("invented_table", "run_id", "runs")

        assertTrue(BATCHES.values.none { names(it, key.owner) && names(it, key.column) })
    }
}
