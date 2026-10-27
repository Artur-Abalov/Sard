// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.persistence

import org.slf4j.LoggerFactory
import java.sql.Connection

/** More than the server needs, found in the role it connects as; [label] is the `reason` of the metric. */
enum class RoleExcess(
    val label: String,
    private val meaning: String,
) {
    SUPERUSER("superuser", "the role is a superuser"),
    SCHEMA_OWNER("schema_owner", "the role owns the schema sard or is a member of its owner"),
    ;

    fun describe() = meaning
}

/** Told what each start found (DB2): the metrics show it (dev.sard.server.metrics.RoleExcessGauges). */
fun interface RoleExcessListener {
    fun found(excess: Set<RoleExcess>)
}

/**
 * The check of the start (DB2): the server needs to read and write rows and nothing more, so a role that is a
 * superuser, or owns the schema (or is a member of the role that does), is one WARN line and a metric, not a refusal:
 * the DBA of an external cluster may have given more on purpose. A superuser is not also counted as the schema's
 * owner, though it is one.
 */
class RoleExcessCheck(
    private val listener: RoleExcessListener,
) {
    private val log = LoggerFactory.getLogger(RoleExcessCheck::class.java)

    fun check(connection: Connection) {
        val (user, found) = read(connection)
        listener.found(found)
        if (found.isNotEmpty()) {
            val what = found.joinToString("; ") { "${it.label} (${it.describe()})" }
            log.warn(
                "the database role {} has more rights than the server needs, reason: {}; use sard_app, which only " +
                    "reads and writes rows, see {}",
                user,
                what,
                DOCS,
            )
        }
    }

    private fun read(connection: Connection): Pair<String, Set<RoleExcess>> =
        connection.createStatement().use { statement ->
            statement
                .executeQuery(
                    """
                    select current_user as name, r.rolsuper as superuser,
                           exists (select from pg_namespace n
                                   where n.nspname = 'sard' and pg_has_role(current_user, n.nspowner, 'MEMBER')) as owner
                    from pg_roles r where r.rolname = current_user
                    """.trimIndent(),
                ).use { row ->
                    check(row.next()) { "the current role is not in pg_roles" }
                    val superuser = row.getBoolean("superuser")
                    row.getString("name") to excesses(superuser, row.getBoolean("owner") && !superuser)
                }
        }

    private fun excesses(
        superuser: Boolean,
        owner: Boolean,
    ): Set<RoleExcess> =
        buildSet {
            if (superuser) add(RoleExcess.SUPERUSER)
            if (owner) add(RoleExcess.SCHEMA_OWNER)
        }
}
