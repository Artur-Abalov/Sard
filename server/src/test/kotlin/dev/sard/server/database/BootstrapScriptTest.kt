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
