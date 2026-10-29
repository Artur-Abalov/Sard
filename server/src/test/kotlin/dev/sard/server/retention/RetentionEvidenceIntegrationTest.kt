// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.retention

import dev.sard.server.TestAdmin
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RunsTestConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.SQLException
import java.time.Duration
import java.time.Instant
import java.time.temporal.ChronoUnit
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertTrue

private const val EVIDENCE = """[{"stage":"restore","ok":true}]"""
private val TERM: Duration = Duration.ofDays(180)

/**
 * Rules "Записи проверок восстановления только дописываются" and "Записи проверок хранятся всегда, улики очищаются по
 * сроку" of docs/specs/server/database-roles.feature, with the whole server and its database. The database's floor
 * counts with the real time of the database, so the records here are placed from the real now.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = [
        "spring.grpc.server.port=0",
        "sard.scheduler.interval=1h",
        "sard.freshness.interval=1h",
        "sard.verification.interval=1h",
    ],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class RetentionEvidenceIntegrationTest(
    @Autowired private val retention: RetentionPass,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val a = SceneTenant(jdbc)
    private val now: Instant = Instant.now().truncatedTo(ChronoUnit.MILLIS)

    @BeforeTest
    fun `the clock stands at the real now`() {
        clock.now = now
    }

    @AfterTest
    fun `drop the tenant and move the clock back`() {
        a.drop()
        clock.now = RUNS_NOW
    }

    private fun ago(
        days: Long,
        seconds: Long = 0,
    ): Instant = now.minus(Duration.ofDays(days)).minusSeconds(seconds)

    /**
     * A source with a fresh passed and a fresh not passed verification, so that nothing old is the last of its kind.
     */
    private fun verifiedSource(): Pair<UUID, UUID> {
        val source = a.source()
        val snapshot = a.oldSnapshot(source)
        a.verification(source, snapshot, ago(1), "passed")
        a.verification(source, snapshot, ago(1), "failed")
        return source to snapshot
    }

    private fun evidenceOf(record: UUID): List<String> =
        jdbc
            .queryForObject(
                "select stages::text || '|' || mismatches::text from restore_verifications where id = ?",
                String::class.java,
                record,
            )!!
            .split("|")

    private fun row(record: UUID): Map<String, Any?> =
        jdbc.queryForMap(
            "select status, level, verified_at, key_holders, job_id, step_id, source_id, snapshot_id from " +
                "restore_verifications where id = ?",
            record,
        )

    @Test
    fun `Запись проверки старше срока не удаляется уборкой`() {
        val (source, snapshot) = verifiedSource()
        val record = a.verification(source, snapshot, ago(181), "failed", evidence = EVIDENCE)
        val before = row(record)

        retention.run()

        assertTrue(a.exists("restore_verifications", record))
        assertEquals(before, row(record))
    }

    @Test
    fun `Улики записи проверки старше срока очищаются, остальное прежнее`() {
        val (source, snapshot) = verifiedSource()
        val record = a.verification(source, snapshot, ago(181), "failed", evidence = EVIDENCE)
        val before = row(record)

        val totals = retention.run()

        assertEquals(listOf("[]", "[]"), evidenceOf(record))
        assertEquals(before, row(record))
        assertEquals(1, totals[RetentionKind.VERIFICATION_EVIDENCE])
    }

    @Test
    fun `Улики записи проверки на границе срока не трогаются`() {
        val (source, snapshot) = verifiedSource()
        val record = a.verification(source, snapshot, ago(180), "failed", evidence = EVIDENCE)

        retention.run()

        assertTrue(evidenceOf(record).all { "restore" in it }, "the evidence on the boundary stays")
    }

    @Test
    fun `Отмена после старого провала не очищает улики этого провала`() {
        val source = a.source()
        val snapshot = a.oldSnapshot(source)
        a.verification(source, snapshot, ago(1), "passed")
        val failure = a.verification(source, snapshot, ago(181), "failed", evidence = EVIDENCE)
        a.verification(source, snapshot, ago(100), "cancelled")

        retention.run()

        assertTrue(evidenceOf(failure).all { "restore" in it }, "a cancelled record proves nothing")
    }

    @Test
    fun `Улики последней проверки источника не очищаются никогда`() {
        for (status in listOf("passed", "infrastructure_failure")) {
            val source = a.source()
            val snapshot = a.oldSnapshot(source)
            val record = a.verification(source, snapshot, ago(400), status, evidence = EVIDENCE)

            retention.run()

            assertTrue(evidenceOf(record).all { "restore" in it }, status)
        }
    }

    @Test
    fun `Закрытое задание старой записи проверки остаётся`() {
        val (source, snapshot) = verifiedSource()
        val record = a.verification(source, snapshot, ago(181), "failed", evidence = EVIDENCE)
        val job = jdbc.queryForObject("select job_id from restore_verifications where id = ?", UUID::class.java, record)

        retention.run()

        assertTrue(a.exists("verification_jobs", requireNotNull(job)))
    }

    @Test
    fun `Запуск и шаг старой записи проверки остаются`() {
        val (source, snapshot) = verifiedSource()
        val oldRun = a.run(source, ago(400), trigger = "verification", status = "failed")
        a.verification(source, snapshot, ago(400), "failed", oldRun, evidence = EVIDENCE)

        retention.run()

        assertTrue(a.exists("runs", oldRun.id))
        assertTrue(a.exists("run_steps", oldRun.steps.single()))
    }

    @Test
    fun `Функция очистки не трогает улики моложе предела базы при любой границе`() {
        val (source, snapshot) = verifiedSource()
        val record = a.verification(source, snapshot, ago(1), "failed", evidence = EVIDENCE)

        val cleared = jdbc.queryForObject("select clear_verification_evidence(?, now(), 500)", Int::class.java, a.id)

        assertEquals(0, cleared)
        assertTrue(evidenceOf(record).all { "restore" in it })
    }

    @Test
    fun `sard_app добавляет запись проверки`() {
        val (source, snapshot) = verifiedSource()

        val record = a.verification(source, snapshot, ago(2), "failed")

        assertTrue(a.exists("restore_verifications", record))
    }

    @Test
    fun `Изменение записи проверки от имени sard_app отвергается, строка прежняя`() {
        val (source, snapshot) = verifiedSource()
        val record = a.verification(source, snapshot, ago(181), "failed", evidence = EVIDENCE)
        val before = row(record)
        val queries =
            listOf(
                "update restore_verifications set status = 'passed' where id = '$record'",
                "update restore_verifications set stages = '[]' where id = '$record'",
                "update restore_verifications set verified_at = now() where id = '$record'",
                "delete from restore_verifications where id = '$record'",
                "truncate restore_verifications",
            )

        for (sql in queries) {
            val failure = assertFailsWith<Exception>(sql) { jdbc.execute(sql) }
            val state =
                generateSequence<Throwable>(failure) { it.cause }
                    .filterIsInstance<SQLException>()
                    .first()
                    .sqlState
            assertEquals("42501", state, sql)
        }

        assertEquals(before, row(record))
        assertTrue("restore" in evidenceOf(record).first())
        assertTrue(
            TestAdmin.of(jdbc).queryForObject(
                "select count(*) from restore_verifications",
                Int::class.java,
            )!! >= 1,
        )
    }
}
