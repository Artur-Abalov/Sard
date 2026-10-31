// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.auth

import dev.sard.server.extension.TenantResolver
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Instant

/** The administrator of the default tenant (the open core has one). Reads hit the database every time. */
interface Administrators {
    /** The stored hash, or null while the admin step is open. */
    fun hash(): String?

    /** False when an administrator exists already. */
    fun create(
        hash: String,
        now: Instant,
    ): Boolean

    /** False when the stored hash is no longer [expected]. */
    fun replaceHash(
        expected: String,
        hash: String,
        now: Instant,
    ): Boolean

    /**
     * True when there was a hash to remove. In the same statement it leaves the mark of the reset, which the next
     * start of the server announces (CORE1 Р32); a reset that removes nothing leaves none, and several leave one.
     */
    fun remove(): Boolean
}

class JdbcAdministrators(
    private val jdbc: JdbcTemplate,
) : Administrators {
    private val tenant = TenantResolver.DEFAULT_TENANT_ID

    override fun hash(): String? =
        jdbc
            .queryForList("select password_hash from administrators where tenant_id = ?", String::class.java, tenant)
            .firstOrNull()

    override fun create(
        hash: String,
        now: Instant,
    ): Boolean =
        jdbc.update(
            "insert into administrators (tenant_id, password_hash, password_changed_at) values (?, ?, ?) " +
                "on conflict do nothing",
            tenant,
            hash,
            Timestamp.from(now),
        ) == 1

    override fun replaceHash(
        expected: String,
        hash: String,
        now: Instant,
    ): Boolean =
        jdbc.update(
            "update administrators set password_hash = ?, password_changed_at = ? " +
                "where tenant_id = ? and password_hash = ?",
            hash,
            Timestamp.from(now),
            tenant,
            expected,
        ) == 1

    // One statement, so the hash goes and the mark appears together or not at all: a data-modifying CTE always runs.
    override fun remove(): Boolean = jdbc.queryForObject(RESET, Int::class.java, tenant) == 1
}

private const val RESET = """
    with removed as (delete from administrators where tenant_id = ? returning tenant_id),
         marked as (
             insert into admin_password_resets (tenant_id) select tenant_id from removed on conflict do nothing
             returning 1
         )
    select count(*) from removed"""
