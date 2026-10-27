// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.database

import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/**
 * Rule "У каждой роли Sard свои атрибуты, и ни одна не суперпользователь" of docs/specs/server/database-roles.feature.
 */
class BootstrapRolesTest {
    private lateinit var cluster: Cluster

    @BeforeTest
    fun `a cluster after bootstrap`() {
        cluster = Cluster()
        cluster.bootstrap()
    }

    @AfterTest
    fun `stop the cluster`() = cluster.close()

    private fun attributes(role: String): Map<String, Boolean> =
        cluster.query(
            "select rolcanlogin, rolsuper, rolcreaterole, rolcreatedb, rolreplication, rolbypassrls, " +
                "rolpassword is null as no_password from pg_authid where rolname = '$role'",
        ) { rs ->
            listOf(
                "rolcanlogin",
                "rolsuper",
                "rolcreaterole",
                "rolcreatedb",
                "rolreplication",
                "rolbypassrls",
                "no_password",
            ).associateWith { rs.getBoolean(it) }
        }

    private fun memberOf(role: String): Set<String> =
        cluster.query(
            "select coalesce(string_agg(r.rolname, ','), '') from pg_auth_members m " +
                "join pg_roles r on r.oid = m.roleid join pg_roles u on u.oid = m.member where u.rolname = '$role'",
        ) { rs ->
            rs
                .getString(1)
                .split(',')
                .filter { it.isNotEmpty() }
                .toSet()
        }

    @Test
    fun `Роль Sard создана с атрибутами D26`() {
        assertEquals(false, attributes("sard_owner").getValue("rolcanlogin"))
        for (role in listOf("sard_migrator", "sard_app", "sard_backup", "sard_monitor")) {
            assertEquals(true, attributes(role).getValue("rolcanlogin"), role)
        }
        assertTrue("sard_owner" in memberOf("sard_migrator"), memberOf("sard_migrator").toString())
        assertTrue(
            memberOf("sard_app").none { it == "sard_owner" || it == "sard_migrator" },
            memberOf("sard_app").toString(),
        )
        assertTrue("pg_read_all_data" in memberOf("sard_backup"), memberOf("sard_backup").toString())
        assertTrue("pg_monitor" in memberOf("sard_monitor"), memberOf("sard_monitor").toString())
        assertTrue(attributes("sard_monitor").getValue("no_password"))
        val readOnly =
            cluster.query(
                "select setconfig::text from pg_db_role_setting s join pg_roles r on r.oid = s.setrole " +
                    "where r.rolname = 'sard_backup' and setdatabase = 0",
            ) { it.getString(1) }
        assertTrue("default_transaction_read_only=on" in readOnly, readOnly)
    }

    @Test
    fun `Роль Sard не суперпользователь и без лишних атрибутов`() {
        for (role in listOf("sard_owner", "sard_migrator", "sard_app", "sard_backup", "sard_monitor")) {
            val a = attributes(role)
            for (name in listOf("rolsuper", "rolcreaterole", "rolcreatedb", "rolreplication", "rolbypassrls")) {
                assertEquals(false, a.getValue(name), "$role $name")
            }
        }
    }
}
