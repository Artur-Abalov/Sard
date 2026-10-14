// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.selfagent

import dev.sard.server.onboarding.Database
import dev.sard.server.recovery.RecoveryCluster
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.postgresql.ds.PGSimpleDataSource
import java.sql.DriverManager
import kotlin.test.Test
import kotlin.test.assertEquals

private const val ROLE_SHAPE =
    "select concat_ws(' ', rolcanlogin, rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls, " +
        "rolconnlimit, rolconfig) from pg_roles where rolname = 'sard_self'"

/** Rule "Роль дампа sard_self возвращается при старте на восстановленной базе": the reconcile statement alone. */
@MutFlowTest
class PostgresDumpRoleTest {
    private fun source(database: Database) =
        PGSimpleDataSource().apply {
            setURL(database.url)
            user = database.user
            password = database.password
        }

    private fun query(
        database: Database,
        sql: String,
    ): List<String> =
        DriverManager.getConnection(database.url, database.user, database.password).use { c ->
            c.createStatement().use { s ->
                s.executeQuery(sql).use { rs -> generateSequence { if (rs.next()) rs.getString(1) else null }.toList() }
            }
        }

    @Test
    fun `Суперпользователь создаёт роль с атрибутами миграции и правом чтения всех данных`() {
        val database = RecoveryCluster.restoredDatabase()

        MutFlow.underTest { PostgresDumpRole(source(database)).reconcile() }

        assertEquals(listOf("t f f f f f 4 {default_transaction_read_only=on}"), query(database, ROLE_SHAPE))
        assertEquals(
            listOf("pg_read_all_data"),
            query(
                database,
                "select r.rolname from pg_auth_members m join pg_roles r on r.oid = m.roleid " +
                    "join pg_roles u on u.oid = m.member where u.rolname = 'sard_self'",
            ),
        )
    }

    @Test
    fun `Повторный вызов ничего не меняет`() {
        val database = RecoveryCluster.restoredDatabase()
        PostgresDumpRole(source(database)).reconcile()

        MutFlow.underTest { PostgresDumpRole(source(database)).reconcile() }

        assertEquals(listOf("t f f f f f 4 {default_transaction_read_only=on}"), query(database, ROLE_SHAPE))
    }

    @Test
    fun `Без прав суперпользователя роль не создаётся и ошибки нет`() {
        val database = RecoveryCluster.restoredDatabase(superuser = false)

        MutFlow.underTest { PostgresDumpRole(source(database)).reconcile() }

        assertEquals(emptyList(), query(database, ROLE_SHAPE))
    }

    @Test
    fun `У PUBLIC отнимаются временные таблицы, создание в схеме и создание больших объектов`() {
        val database = RecoveryCluster.restoredDatabase()

        MutFlow.underTest { PostgresDumpRole(source(database)).reconcile() }

        assertEquals(
            listOf("false"),
            query(database, "select has_database_privilege('public', current_database(), 'TEMPORARY')::text"),
        )
        assertEquals(
            listOf("false"),
            query(database, "select has_schema_privilege('public', 'public', 'CREATE')::text"),
        )
        assertEquals(
            listOf("false"),
            query(database, "select has_function_privilege('public', 'lo_create(oid)', 'EXECUTE')::text"),
        )
    }
}
