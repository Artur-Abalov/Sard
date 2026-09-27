// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.TestcontainersConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.dao.DataIntegrityViolationException
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.time.Instant
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/**
 * F4: enrollment_tokens_label_check and enrollment_tokens_revocation_check
 * (V202609271600__enrollment_revocation.sql) enforce at the database level what decisions 2 and 6
 * of docs/specs/server/agent-enrollment.feature require of every row, not only of rows written
 * through Enroll and revoke.
 */
@SpringBootTest
@Import(TestcontainersConfiguration::class)
class EnrollmentTokenSchemaIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val acme = UUID.randomUUID()

    @BeforeTest
    fun `a fresh tenant`() {
        jdbc.insertTenant(acme)
    }

    @AfterTest
    fun `drop everything of the tenant`() {
        jdbc.deleteEnrollmentTenantData(acme)
    }

    private fun insertUsedToken(): UUID {
        val id = UUID.randomUUID()
        val now = Timestamp.from(Instant.now())
        jdbc.update(
            "insert into enrollment_tokens (id, tenant_id, token_hash, created_at, expires_at, used_at, label) " +
                "values (?, ?, ?, ?, ?, ?, ?)",
            id,
            acme,
            ByteArray(32),
            now,
            Timestamp.from(now.toInstant().plusSeconds(3600)),
            now,
            "",
        )
        return id
    }

    @Test
    fun `the database refuses a token that is both used and revoked`() {
        val id = insertUsedToken()
        assertFailsWith<DataIntegrityViolationException> {
            jdbc.update(
                "update enrollment_tokens set revoked_at = ? where id = ?",
                Timestamp.from(Instant.now()),
                id,
            )
        }
    }

    @Test
    fun `the label and revocation checks exist by name`() {
        val names =
            jdbc.queryForList(
                "select conname from pg_constraint where conname in " +
                    "('enrollment_tokens_label_check', 'enrollment_tokens_revocation_check')",
                String::class.java,
            )
        assertEquals(
            setOf("enrollment_tokens_label_check", "enrollment_tokens_revocation_check"),
            names.toSet(),
        )
    }
}
