// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * Rules "bootstrap.sql готовит внешний кластер одним запуском" and "Роли ... создаёт разовый шаг db-init" of
 * docs/specs/server/database-roles.feature, on the PostgreSQL of the tests (the CI runs the same script on 16, 17 and
 * 18).
 */
class BootstrapScriptTest {
    private lateinit var cluster: Cluster

    @BeforeTest
    fun `a clean cluster`() {
        cluster = Cluster()
    }

    @AfterTest
    fun `stop the cluster`() = cluster.close()

    private fun exec(vararg command: String) = cluster.container.execInContainer(*command)

    @Test
    fun `Роли sard_self в кластере нет, а sard_app не член чужих ролей`() {
        cluster.bootstrap()

        assertEquals(0, cluster.query("select count(*) from pg_roles where rolname = 'sard_self'") { it.getInt(1) })
        val inherited =
            cluster.query(
                """select coalesce(string_agg(g.rolname, ','), '') from pg_roles g
                   where g.rolname <> 'sard_app' and pg_has_role('sard_app', g.oid, 'MEMBER')
                     and g.rolname in (
                         'sard_owner', 'sard_migrator', 'pg_read_all_data', 'pg_write_all_data', 'pg_monitor')""",
            ) { it.getString(1) }
        assertEquals("", inherited)
    }

    private val loFunctions =
        "lo_create(oid), lo_creat(integer), lo_from_bytea(oid, bytea), lo_import(text), lo_import(text, oid)"

    private fun admin(
        vararg sql: String,
        database: String = cluster.container.databaseName,
    ) = cluster.admin(database).use { c -> c.createStatement().use { s -> sql.forEach(s::execute) } }

    /** The privileges on the functions of pg_catalog are kept per database. */
    private fun revokeLargeObjectFunctionsFromPublic(database: String) =
        admin("revoke execute on function $loFunctions from public", database = database)

    /** A role with CREATEROLE and the rights bootstrap.sql asks of it, owner of a database of its own (its name). */
    private fun createroleDatabase(): String {
        admin(
            "create role dba login createrole password 'dba'",
            "grant pg_read_all_data to dba with admin option",
            "grant pg_monitor to dba with admin option",
        )
        val db = cluster.newDatabase()
        admin("alter database $db owner to dba")
        return db
    }

    private fun runAsDba(database: String) =
        java.sql.DriverManager.getConnection(cluster.url(database), "dba", "dba").use {
            it.createStatement().execute(BootstrapScript.text)
        }

    private fun members(role: String) =
        cluster.query(
            "select coalesce(string_agg(g.rolname, ',' order by g.rolname), '') from pg_auth_members m " +
                "join pg_roles g on g.oid = m.roleid where m.member = '$role'::regrole",
        ) { it.getString(1) }

    private fun nothingCreated(database: String) {
        assertEquals(0, cluster.query("select count(*) from pg_roles where rolname like 'sard\\_%'") { it.getInt(1) })
        assertEquals(
            0,
            cluster.query("select count(*) from pg_namespace where nspname = 'sard'", database) { it.getInt(1) },
        )
    }

    @Test
    fun `sard_app, уже состоявший в чужих ролях, после запуска суперпользователя не член ничего`() {
        admin(
            "create role sard_owner nologin",
            "create role sard_app login",
            "grant sard_owner to sard_app",
            "grant pg_write_all_data to sard_app",
        )

        cluster.bootstrap()

        assertEquals("", members("sard_app"))
    }

    @Test
    fun `Роль с CREATEROLE при EXECUTE у PUBLIC на lo_ функции получает отказ с лечением и ничего не создаёт`() {
        val byDba = createroleDatabase()

        val failure = runCatching { runAsDba(byDba) }.exceptionOrNull()

        val message = failure?.message.orEmpty()
        listOf("lo_create(oid)", "lo_creat(integer)", "lo_from_bytea(oid, bytea)")
            .forEach { assertTrue(it in message, "$it in: $message") }
        assertTrue("REVOKE EXECUTE ON FUNCTION" in message && "FROM PUBLIC" in message, message)
        nothingCreated(byDba)
    }

    @Test
    fun `Роль с CREATEROLE, у которой нет ADMIN на роль, где состоит sard_app, получает отказ с именем роли`() {
        val byDba = createroleDatabase()
        revokeLargeObjectFunctionsFromPublic(byDba)
        admin(
            "create role sard_app login",
            "grant sard_app to dba with admin option",
            "grant pg_write_all_data to sard_app",
        )

        val failure = runCatching { runAsDba(byDba) }.exceptionOrNull()

        val message = failure?.message.orEmpty()
        assertTrue("pg_write_all_data" in message && "REVOKE pg_write_all_data FROM sard_app" in message, message)
        assertEquals(
            0,
            cluster.query("select count(*) from pg_namespace where nspname = 'sard'", byDba) { it.getInt(1) },
        )
        assertEquals("pg_write_all_data", members("sard_app"))
    }

    @Test
    fun `Повторный запуск скрипта bootstrap ничего не меняет`() {
        cluster.bootstrap()
        val first = cluster.catalog()

        cluster.bootstrap()

        assertEquals(first, cluster.catalog())
        assertTrue("role sard_app" in first && "schema sard owner=sard_owner" in first, first)
    }

    @Test
    fun `Скрипт bootstrap не задаёт паролей`() {
        cluster.admin().use { it.createStatement().execute(BootstrapScript.text) }

        val withPassword =
            cluster.query("select count(*) from pg_authid where rolname like 'sard\\_%' and rolpassword is not null") {
                it.getInt(1)
            }
        assertEquals(0, withPassword)
        assertFalse(Regex("""PASSWORD\s+'""", RegexOption.IGNORE_CASE).containsMatchIn(BootstrapScript.text))
    }

    @Test
    fun `Скрипт bootstrap без нужных прав не создаёт ничего`() {
        cluster.admin().use { it.createStatement().execute("create role weak login password 'weak'") }

        val failure =
            runCatching {
                java.sql.DriverManager
                    .getConnection(
                        cluster.url(),
                        "weak",
                        "weak",
                    ).use { it.createStatement().execute(BootstrapScript.text) }
            }.exceptionOrNull()

        assertTrue(failure != null && "SUPERUSER or CREATEROLE" in failure.message.orEmpty(), failure?.message)
        assertEquals(0, cluster.query("select count(*) from pg_roles where rolname like 'sard\\_%'") { it.getInt(1) })
        assertEquals(0, cluster.query("select count(*) from pg_namespace where nspname = 'sard'") { it.getInt(1) })
    }

    @Test
    fun `Скрипт bootstrap под ролью с CREATEROLE без SUPERUSER даёт то же, что под суперпользователем`() {
        // Roles belong to the cluster, so the superuser's run is in a cluster of its own.
        val expected =
            Cluster().use {
                it.bootstrap()
                it.catalog()
            }
        cluster.admin().use {
            it.createStatement().use { s ->
                s.execute("create role dba login createrole password 'dba'")
                s.execute("grant pg_read_all_data to dba with admin option")
                s.execute("grant pg_monitor to dba with admin option")
            }
        }
        val byDba = cluster.newDatabase()
        cluster.admin().use { it.createStatement().use { s -> s.execute("alter database $byDba owner to dba") } }
        revokeLargeObjectFunctionsFromPublic(byDba)

        fun run() =
            java.sql.DriverManager.getConnection(cluster.url(byDba), "dba", "dba").use {
                it.createStatement().execute(BootstrapScript.text)
            }

        run()
        val first = cluster.catalog(byDba)
        run()

        assertEquals(first, cluster.catalog(byDba), "the rerun changes nothing")
        assertEquals(expected, first.replace("dba", cluster.container.username))
    }

    @Test
    fun `Повторный db-init на базе, развёрнутой суперпользователем, возвращает владение sard_owner`() {
        val installed = cluster.newDatabase()
        cluster.bootstrap(installed)
        Preparation.migrate(cluster.url(installed))
        val restored = cluster.newDatabase()
        val dump = "/tmp/$installed.dump"
        val user = cluster.container.username
        assertEquals(0, exec("pg_dump", "-U", user, "-d", installed, "-Fc", "-f", dump).exitCode)
        val restore = exec("pg_restore", "-U", user, "-d", restored, "--no-owner", "--no-acl", "--exit-on-error", dump)
        assertEquals(0, restore.exitCode, restore.stderr)
        val owners =
            cluster.query(
                "select string_agg(distinct c.relowner::regrole::text, ',') from pg_class c where c.relnamespace = " +
                    "'sard'::regnamespace",
                restored,
            ) {
                it.getString(1)
            }
        assertEquals(user, owners, "the restore by the superuser makes it the owner")

        cluster.bootstrap(restored)

        val owned =
            cluster.query(
                "select string_agg(distinct c.relowner::regrole::text, ',') from pg_class c where c.relnamespace = " +
                    "'sard'::regnamespace",
                restored,
            ) {
                it.getString(1)
            }
        assertEquals("sard_owner", owned)
        val history =
            cluster.query(
                "select concat_ws(',', has_table_privilege('sard_app', 'sard.flyway_schema_history', 'SELECT')," +
                    "has_table_privilege('sard_app', 'sard.flyway_schema_history', 'INSERT')," +
                    "has_table_privilege('sard_app', 'sard.flyway_schema_history', 'UPDATE')," +
                    "has_table_privilege('sard_app', 'sard.flyway_schema_history', 'DELETE')," +
                    "has_table_privilege('sard_app', 'sard.flyway_schema_history', 'TRUNCATE'))",
                restored,
            ) {
                it.getString(1)
            }
        assertEquals("t,f,f,f,f", history, "sard_app only reads the history after the rerun")
        assertEquals(
            cluster.catalog(installed).replace(installed, "DB"),
            cluster.catalog(restored).replace(restored, "DB"),
        )
    }
}
