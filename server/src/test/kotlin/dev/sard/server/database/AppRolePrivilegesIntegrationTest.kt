// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import java.sql.SQLException
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * Rules "sard_app только читает и пишет строки" and "Служебные функции" of docs/specs/server/database-roles.feature.
 */
class AppRolePrivilegesIntegrationTest {
    private lateinit var cluster: Cluster

    @BeforeTest
    fun `a prepared installation`() {
        cluster = Cluster()
        cluster.bootstrap()
        Preparation.migrate(cluster.url())
    }

    @AfterTest
    fun `stop the cluster`() = cluster.close()

    /** The SQLSTATE of the failure of [sql] run by [role] in a session that may write, or null when it succeeded. */
    private fun failureOf(
        role: String,
        sql: String,
    ): String? =
        cluster.connect(role).use { c ->
            c.createStatement().use { it.execute("set default_transaction_read_only = off") }
            runCatching { c.createStatement().use { it.execute(sql) } }.exceptionOrNull()?.let {
                (it as SQLException)
                    .sqlState
            }
        }

    private fun strings(sql: String): List<String> =
        cluster.admin().use { c ->
            c.createStatement().use { s ->
                s.executeQuery(sql).use { rs -> generateSequence { if (rs.next()) rs.getString(1) else null }.toList() }
            }
        }

    @Test
    fun `DDL от имени sard_app отвергается и каталог не меняется`() {
        val queries =
            listOf(
                "create table sard.x (i int)",
                "alter table runs add column x int",
                "drop table runs",
                "truncate runs",
                "create index x on runs (queued_at)",
                "create function sard.f() returns int as 'select 1' language sql",
                "alter table runs owner to sard_app",
            )
        val before = cluster.catalog()
        for (sql in queries) assertEquals("42501", failureOf("sard_app", sql), sql)
        assertEquals(before, cluster.catalog())
    }

    /**
     * The one query of the specification's table that PostgreSQL does not answer with 42501: a role that holds a
     * privilege
     * on a table but not the right to grant it gets the warning "no privileges were granted", not an error. It changes
     * nothing, which is what the scenario is for.
     */
    @Test
    fun `GRANT от имени sard_app ничего не выдаёт`() {
        val before = cluster.catalog()

        val warnings =
            cluster.connect("sard_app").use { c ->
                c.createStatement().use { s ->
                    s.execute("grant select on runs to public")
                    s.warnings?.message
                }
            }

        assertTrue(warnings.orEmpty().contains("no privileges were granted"), warnings)
        assertEquals(before, cluster.catalog())
    }

    @Test
    fun `sard_app не создаёт временных таблиц и больших объектов`() {
        assertEquals("42501", failureOf("sard_app", "create temp table x (i int)"))
        assertEquals("42501", failureOf("sard_app", "select lo_create(0)"))
    }

    @Test
    fun `sard_app не может подменить служебную функцию`() {
        val functions = "select oid::regprocedure::text from pg_proc where pronamespace = 'sard'::regnamespace"
        for (signature in strings(functions)) {
            val name = signature.substringBefore("(").substringAfter(".")
            val sql = "create or replace function sard.$name() returns int as 'select 1' language sql"
            assertEquals("42501", failureOf("sard_app", sql), signature)
        }
    }

    @Test
    fun `Служебная функция выполняется с закреплённым search_path`() {
        val unsafe =
            strings(
                """
                select p.oid::regprocedure::text || ' ' || coalesce(c, '-') from pg_proc p
                left join lateral (select cfg from unnest(p.proconfig) cfg where cfg like 'search_path=%') s(c) on true
                where p.pronamespace = 'sard'::regnamespace and p.prosecdef
                """.trimIndent(),
            )

        assertTrue(unsafe.isNotEmpty(), "there are functions with SECURITY DEFINER")
        for (line in unsafe) {
            val path = line.substringAfter("search_path=", "")
            assertTrue(path.isNotEmpty(), "no search_path: $line")
            val schemas = path.split(",").map { it.trim().trim('"') }
            assertTrue("public" !in schemas, line)
            for (schema in schemas) {
                val writable = strings("select has_schema_privilege('sard_app', '$schema', 'CREATE')::text")
                assertEquals(listOf("false"), writable, "$schema in $line")
            }
        }
    }

    @Test
    fun `Служебные функции вызывают только sard_app и владелец`() {
        val rights =
            strings(
                """
                select p.oid::regprocedure::text || ' app=' || has_function_privilege('sard_app', p.oid, 'EXECUTE') ||
                       ' owner=' || has_function_privilege('sard_owner', p.oid, 'EXECUTE') ||
                       ' public=' || coalesce((select bool_or(a.grantee = 0) from aclexplode(p.proacl) a), false)
                from pg_proc p where p.pronamespace = 'sard'::regnamespace
                """.trimIndent(),
            )

        assertTrue(rights.isNotEmpty())
        for (line in rights) assertTrue(line.endsWith("app=true owner=true public=false"), line)
    }

    @Test
    fun `sard_app не меняет предел срока улик в базе`() {
        for (sql in listOf(
            "update retention_floors set min_days = 1",
            "delete from retention_floors",
            "insert into retention_floors values ('x', 1)",
            "truncate retention_floors",
        )) {
            assertEquals("42501", failureOf("sard_app", sql), sql)
        }
        assertEquals(
            listOf("180"),
            strings("select min_days::text from sard.retention_floors where name = 'verification_evidence'"),
        )
    }

    @Test
    fun `Функции секций ждут блокировку не дольше 2 секунд, и DEFAULT-секции нет`() {
        val settings =
            strings(
                """
                select p.proname || ' ' || coalesce((select cfg from unnest(p.proconfig) cfg where cfg like 'lock_timeout=%'), '-')
                from pg_proc p where p.pronamespace = 'sard'::regnamespace and p.proname like '%step_log_partition'
                order by 1
                """.trimIndent(),
            )

        assertEquals(
            listOf("create_step_log_partition lock_timeout=2s", "drop_step_log_partition lock_timeout=2s"),
            settings,
        )
        assertEquals(
            emptyList(),
            strings(
                "select c.relname from pg_inherits i join pg_class c on c.oid = i.inhrelid where i.inhparent = " +
                    "'sard.step_logs'::regclass and pg_get_expr(c.relpartbound, c.oid) = 'DEFAULT'",
            ),
        )
    }

    @Test
    fun `sard_app не удаляет и не отцепляет секцию напрямую`() {
        val partition =
            strings(
                "select c.relname from pg_inherits i join pg_class c on c.oid = i.inhrelid where i.inhparent = " +
                    "'sard.step_logs'::regclass limit 1",
            ).single()

        assertEquals("42501", failureOf("sard_app", "drop table sard.$partition"))
        assertEquals("42501", failureOf("sard_app", "alter table sard.step_logs detach partition sard.$partition"))
    }

    @Test
    fun `Секция, созданная функцией, принадлежит sard_owner`() {
        val created =
            cluster.connect("sard_app").use { c ->
                c.createStatement().use { s ->
                    s.executeQuery("select create_step_log_partition('2031-08-15')").use {
                        it.next()
                        it.getString(1)
                    }
                }
            }

        assertEquals("step_logs_2031_08", created)
        assertEquals(
            listOf("sard_owner"),
            strings("select relowner::regrole::text from pg_class where relname = 'step_logs_2031_08'"),
        )
    }

    @Test
    fun `Две сессии создают одну секцию без ошибки, и секция одна`() {
        val pool =
            java.util.concurrent.Executors
                .newFixedThreadPool(2)
        val start = java.util.concurrent.CountDownLatch(1)
        val creations =
            (1..2).map {
                pool.submit<Unit> {
                    cluster.connect("sard_app").use { c ->
                        start.await()
                        c.createStatement().use { s ->
                            s.executeQuery("select create_step_log_partition('2032-01-01')").use { rs -> rs.next() }
                        }
                    }
                }
            }
        start.countDown()
        creations.forEach { it.get(30, java.util.concurrent.TimeUnit.SECONDS) }
        pool.shutdown()

        assertEquals(
            listOf("step_logs_2032_01"),
            strings("select relname from pg_class where relname = 'step_logs_2032_01'"),
        )
    }

    @Test
    fun `sard_backup ничего не пишет даже без режима только чтения`() {
        val queries =
            listOf(
                "update tenants set name = name",
                "create table sard.x (i int)",
                "create temp table x (i int)",
                "select lo_create(0)",
            )

        for (sql in queries) assertEquals("42501", failureOf("sard_backup", sql), sql)
    }

    @Test
    fun `sard_backup читает каждую таблицу схемы sard`() {
        val tables =
            strings("select relname from pg_class where relnamespace = 'sard'::regnamespace and relkind in ('r', 'p')")

        assertTrue(tables.size > 20, tables.toString())
        for (table in tables) assertEquals(null, failureOf("sard_backup", "select count(*) from sard.$table"), table)
    }
}
