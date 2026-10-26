// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import org.junit.jupiter.api.io.TempDir
import java.io.ByteArrayOutputStream
import java.io.PrintStream
import java.nio.file.Files
import java.nio.file.Path
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** Rules "Все объекты Sard в схеме sard, и ей владеет sard_owner" and "Права на новую таблицу выдаются сами". */
class MigratorIntegrationTest {
    @TempDir
    lateinit var tmp: Path

    private lateinit var cluster: Cluster

    @BeforeTest
    fun `a prepared database`() {
        cluster = Cluster()
        cluster.bootstrap()
    }

    @AfterTest
    fun `stop the cluster`() = cluster.close()

    private fun settings(extensions: List<String> = emptyList()) =
        MigrationSettings(cluster.url(), "sard_migrator", cluster.password("sard_migrator"), extensions)

    private fun migrate(extensions: List<String> = emptyList()): MigrationOutcome =
        Migrator(settings(extensions), PrintStream(ByteArrayOutputStream())).run()

    private fun strings(sql: String): List<String> =
        cluster.admin().use { c ->
            c.createStatement().use { s ->
                s.executeQuery(sql).use { rs -> generateSequence { if (rs.next()) rs.getString(1) else null }.toList() }
            }
        }

    @Test
    fun `История миграций ядра лежит в схеме sard`() {
        migrate()

        val homes =
            strings("select table_schema from information_schema.tables where table_name = 'flyway_schema_history'")
        assertEquals(listOf("sard"), homes)
        val applied = cluster.query("select count(*) from sard.flyway_schema_history where success") { it.getInt(1) }
        assertTrue(applied > 20, "applied $applied")
    }

    @Test
    fun `Каждая таблица, индекс и функция Sard лежат в схеме sard`() {
        migrate()

        val outside =
            strings(
                """
                select n.nspname || '.' || c.relname from pg_class c join pg_namespace n on n.oid = c.relnamespace
                where n.nspname not in ('pg_catalog', 'information_schema', 'pg_toast', 'sard')
                  and c.relkind in ('r', 'p', 'i', 'I', 'S', 'v', 'm')
                union all
                select n.nspname || '.' || p.proname from pg_proc p join pg_namespace n on n.oid = p.pronamespace
                where n.nspname not in ('pg_catalog', 'information_schema', 'sard')
                """.trimIndent(),
            )
        assertEquals(emptyList(), outside)
        assertEquals(emptyList(), strings("select tablename from pg_tables where schemaname = 'public'"))
    }

    @Test
    fun `Владелец схемы sard и каждого её объекта - sard_owner`() {
        migrate()

        val owners =
            strings(
                """
                select distinct owner from (
                    select nspowner::regrole::text as owner from pg_namespace where nspname = 'sard'
                    union all select relowner::regrole::text from pg_class where relnamespace = 'sard'::regnamespace
                    union all select proowner::regrole::text from pg_proc where pronamespace = 'sard'::regnamespace
                ) o
                """.trimIndent(),
            )
        assertEquals(listOf("sard_owner"), owners)
    }

    @Test
    fun `PUBLIC не создаёт объектов`() {
        migrate()

        val cases =
            listOf(
                "sard_app" to "public",
                "sard_app" to "sard",
                "sard_backup" to "public",
                "sard_monitor" to "public",
            )
        for ((role, schema) in cases) {
            // sard_monitor has no password: the superuser acts as it.
            cluster.admin().use { c ->
                c.createStatement().use {
                    it.execute("set session authorization $role; set default_transaction_read_only = off")
                }
                val failure =
                    runCatching {
                        c.createStatement().use {
                            it.execute("create table $schema.x (i int)")
                        }
                    }.exceptionOrNull()
                assertEquals("42501", (failure as? java.sql.SQLException)?.sqlState, "$role $schema: $failure")
            }
        }
    }

    @Test
    fun `Сеанс роли Sard начинается со схемы sard в search_path`() {
        migrate()

        for (role in listOf("sard_migrator", "sard_app", "sard_backup")) {
            val path =
                cluster.connect(role).use { c ->
                    c.createStatement().use { s ->
                        s.executeQuery("show search_path").use {
                            it.next()
                            it.getString(1)
                        }
                    }
                }
            assertEquals("sard", path, role)
        }
    }

    @Test
    fun `Повторный migrate без новых миграций ничего не меняет`() {
        migrate()
        val before =
            cluster.query(
                "select string_agg(installed_rank || version || checksum, ',' order by installed_rank) from " +
                    "sard.flyway_schema_history",
            ) {
                it.getString(1)
            }

        val again = migrate()

        assertEquals(MigrationOutcome(0, 0), again)
        val after =
            cluster.query(
                "select string_agg(installed_rank || version || checksum, ',' order by installed_rank) from " +
                    "sard.flyway_schema_history",
            ) {
                it.getString(1)
            }
        assertEquals(before, after)
    }

    /** A directory of migrations of a test extension: [sql] is the one migration of version [version]. */
    private fun extension(
        sql: String,
        version: String = "1",
    ): String {
        val dir = Files.createDirectories(tmp.resolve("extension-$version"))
        Files.writeString(dir.resolve("V${version}__test.sql"), sql)
        return "filesystem:$dir"
    }

    private fun failure(
        role: String,
        sql: String,
    ): String? =
        cluster.connect(role).use { c ->
            runCatching { c.createStatement().use { it.execute("set default_transaction_read_only = off; $sql") } }
                .exceptionOrNull()
                ?.let { (it as java.sql.SQLException).sqlState }
        }

    @Test
    fun `Таблица новой миграции сразу доступна sard_app на чтение и запись`() {
        migrate(listOf(extension("create table t (i int primary key);")))

        val statements = listOf("insert into t values (1)", "select * from t", "update t set i = 2", "delete from t")
        for (sql in statements) assertEquals(null, failure("sard_app", sql), sql)
    }

    @Test
    fun `Таблица новой миграции принадлежит sard_owner`() {
        migrate(listOf(extension("create table t (i int primary key);")))

        assertEquals(listOf("sard_owner"), strings("select tableowner from pg_tables where tablename = 't'"))
        assertEquals(
            listOf("sard_owner"),
            strings("select indexname from pg_indexes where tablename = 't'").map { "sard_owner" }.distinct(),
        )
        assertEquals(
            listOf("sard_owner"),
            strings("select relowner::regrole::text from pg_class where relname = 't_pkey'"),
        )
    }

    @Test
    fun `Таблицу новой миграции читает sard_backup`() {
        migrate(listOf(extension("create table t (i int primary key);")))

        assertEquals(null, failure("sard_backup", "select count(*) from t"))
    }

    @Test
    fun `Без расширений таблицы истории расширений нет и сервер стартует`() {
        migrate()

        assertEquals(
            emptyList(),
            strings("select table_name from information_schema.tables where table_name like 'flyway_schema_history_%'"),
        )
    }

    @Test
    fun `Миграция расширения применяется после всех миграций ядра`() {
        val outcome = migrate(listOf(extension("create table e (i int);")))

        assertEquals(1, outcome.extensions)
        val extensionAt =
            cluster.query("select min(installed_on) from sard.flyway_schema_history_extensions where version = '1'") {
                it.getTimestamp(1)
            }
        val coreAt = cluster.query("select max(installed_on) from sard.flyway_schema_history") { it.getTimestamp(1) }
        assertTrue(!extensionAt.before(coreAt), "$extensionAt before $coreAt")
    }

    @Test
    fun `История ядра не содержит миграций расширения`() {
        migrate(listOf(extension("create table e (i int);")))

        assertEquals(emptyList(), strings("select script from sard.flyway_schema_history where script like '%test%'"))
    }

    @Test
    fun `Объект миграции расширения принадлежит sard_owner и доступен sard_app`() {
        migrate(listOf(extension("create table e (i int);")))

        assertEquals(null, failure("sard_app", "insert into e values (1)"))
        assertEquals(null, failure("sard_app", "select * from e"))
        assertEquals(listOf("sard_owner"), strings("select tableowner from pg_tables where tablename = 'e'"))
    }

    @Test
    fun `Сеанс миграций идёт с lock_timeout 5 секунд`() {
        migrate(
            listOf(
                extension("create table seen as select current_setting('lock_timeout') as value, current_user as who;"),
            ),
        )

        assertEquals(listOf("5s"), strings("select value from sard.seen"))
        assertEquals(listOf("sard_owner"), strings("select who from sard.seen"))
    }

    @Test
    fun `Миграция, ждущая блокировку дольше 5 секунд, завершается ошибкой без изменений`() {
        migrate()
        val before = cluster.query("select count(*) from sard.flyway_schema_history") { it.getInt(1) }
        val blocker = cluster.admin()
        blocker.autoCommit = false
        blocker.createStatement().use { it.execute("lock table sard.runs in access exclusive mode") }
        val started = System.nanoTime()

        val refusal =
            runCatching { migrate(listOf(extension("alter table runs add column x int;"))) }.exceptionOrNull()
        val seconds = (System.nanoTime() - started) / 1_000_000_000.0

        blocker.rollback()
        blocker.close()
        assertTrue(refusal is MigrationRefused, refusal.toString())
        assertTrue(
            "lock timeout" in refusal.message.orEmpty().lowercase() || "55P03" in refusal.message.orEmpty(),
            refusal.message,
        )
        assertTrue(seconds < 10, "took $seconds s")
        assertEquals(before, cluster.query("select count(*) from sard.flyway_schema_history") { it.getInt(1) })
        assertEquals(
            emptyList(),
            strings(
                "select column_name from information_schema.columns where table_name = 'runs' and column_name = 'x'",
            ),
        )
    }

    @Test
    fun `База без db-init не мигрируется, сообщение называет db-init и раздел 16`() {
        Cluster().use { bare ->
            val refusal =
                runCatching {
                    Migrator(
                        MigrationSettings(bare.url(), "sard_migrator", "x"),
                        PrintStream(ByteArrayOutputStream()),
                    ).run()
                }.exceptionOrNull()

            assertTrue(refusal is MigrationRefused, refusal.toString())
            assertTrue(
                "db-init" in refusal.message.orEmpty() && "docs/operator/16-postgresql.md" in refusal.message.orEmpty(),
                refusal.message,
            )
        }
    }

    @Test
    fun `Неготовая база - нет схемы sard - называет причину`() {
        Cluster().use { half ->
            half.bootstrap()
            half.admin().use { it.createStatement().execute("drop schema sard") }

            val refusal = runCatching { migrateOn(half) }.exceptionOrNull()

            assertTrue(refusal is MigrationRefused && "schema sard" in refusal.message.orEmpty(), refusal.toString())
        }
    }

    private fun migrateOn(other: Cluster) =
        Migrator(
            MigrationSettings(other.url(), "sard_migrator", other.password("sard_migrator")),
            PrintStream(ByteArrayOutputStream()),
        ).run()
}
