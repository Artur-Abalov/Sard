// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.enrollment

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.extension.TenantResolver.Companion.DEFAULT_TENANT_ID
import dev.sard.server.persistence.EnrollmentTokenRecord
import dev.sard.server.persistence.HibernateTenantBridge
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.CertificateAuthority
import jakarta.persistence.EntityManagerFactory
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.boot.test.context.TestConfiguration
import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Connection
import java.sql.SQLException
import java.time.Clock
import java.time.Duration
import java.time.ZoneOffset
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertFalse
import kotlin.test.assertNull

/** PostgreSQL SQLSTATE for a write inside a read-only transaction. */
private const val READ_ONLY_SQL_TRANSACTION = "25006"

@TestConfiguration(proxyBeanMethods = false)
class FixedClockConfiguration {
    @Bean
    fun clock(): Clock = Clock.fixed(ENROLLMENT_NOW, ZoneOffset.UTC)
}

/** Tokens are stored as hashes and found before the tenant is known (ADR 0013), nowhere else. */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, FixedClockConfiguration::class)
class EnrollmentTokensIntegrationTest(
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val ca: CertificateAuthority,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val emf: EntityManagerFactory,
) {
    private val acme = UUID.randomUUID()

    @BeforeTest
    fun `create a second tenant`() {
        jdbc.insertTenant(acme)
    }

    @AfterTest
    fun `drop the tokens and the tenant`() {
        jdbc.update("delete from enrollment_tokens where tenant_id in (?, ?)", acme, DEFAULT_TENANT_ID)
        jdbc.update("delete from tenants where id = ?", acme)
    }

    @Test
    fun `a created token is stored only as the hash of its secret`() {
        val issued = tokens.create(acme, Duration.ofHours(24))
        val token = EnrollmentToken.parse(issued.reveal())
        assertEquals(ca.fingerprint(), token.fingerprint)

        val row = jdbc.queryForMap("select * from enrollment_tokens where id = ?", issued.id)
        assertEquals(acme, row["tenant_id"])
        assertContentEquals(token.secret.hash(), row["token_hash"] as ByteArray)
        assertEquals(ENROLLMENT_NOW.plus(Duration.ofHours(24)), (row["expires_at"] as java.sql.Timestamp).toInstant())
        assertEquals(ENROLLMENT_NOW, (row["created_at"] as java.sql.Timestamp).toInstant())
        assertEquals(listOf(null, null), listOf(row["used_at"], row["agent_id"]))
        assertEquals(ENROLLMENT_NOW.plus(Duration.ofHours(24)), issued.expiresAt)

        val secret = issued.reveal().substringAfter("sard_").substringBefore('.')
        val rowSql = "select t::text from enrollment_tokens t where id = ?"
        val text = jdbc.queryForObject(rowSql, String::class.java, issued.id)
        assertFalse(secret in text!!, "the secret reached the database")
        assertFalse(secret in issued.toString(), issued.toString())
    }

    @Test
    fun `a token must live for a positive duration`() {
        assertFailsWith<IllegalArgumentException> { tokens.create(acme, Duration.ZERO) }
        assertFailsWith<IllegalArgumentException> { tokens.create(acme, Duration.ofSeconds(-1)) }
    }

    @Test
    fun `the system lookup finds a token of any tenant by its hash`() {
        val foreign = tokens.create(acme, Duration.ofHours(1))
        val own = tokens.create(DEFAULT_TENANT_ID, Duration.ofHours(1))
        assertEquals(TokenOwner(foreign.id, acme), tokens.ownerOf(hashOf(foreign)))
        assertEquals(TokenOwner(own.id, DEFAULT_TENANT_ID), tokens.ownerOf(hashOf(own)))
        assertNull(tokens.ownerOf(ByteArray(32)))
    }

    @Test
    fun `tenant-scoped reads still do not see a foreign token`() {
        val foreign = tokens.create(acme, Duration.ofHours(1))
        val own = tokens.create(DEFAULT_TENANT_ID, Duration.ofHours(1))

        emf.createEntityManager().use { em ->
            assertNull(em.find(EnrollmentTokenRecord::class.java, foreign.id), "the resolver's tenant saw acme")
            val all = em.createQuery("select t.id from EnrollmentTokenRecord t", UUID::class.java).resultList
            assertEquals(listOf(own.id), all)
        }
        val seenByAcme =
            sessions.inTenant(acme) { s ->
                s.createSelectionQuery("select t.id from EnrollmentTokenRecord t", UUID::class.java).list()
            }
        assertEquals(listOf(foreign.id), seenByAcme)
    }

    @Test
    fun `the system session cannot write`() {
        tokens.create(acme, Duration.ofHours(1))
        val error =
            assertFailsWith<Exception> {
                sessions.system { s ->
                    // Straight to JDBC: the guarantee is the database's, not Hibernate's.
                    s.doWork { c: Connection ->
                        c.createStatement().use { it.executeUpdate("delete from enrollment_tokens") }
                    }
                }
            }
        val causes = generateSequence<Throwable>(error) { it.cause }
        val states = causes.filterIsInstance<SQLException>().map { it.sqlState }
        assertEquals(READ_ONLY_SQL_TRANSACTION, states.firstOrNull(), error.toString())
        assertEquals(1, jdbc.queryForObject("select count(*) from enrollment_tokens", Int::class.java))
    }

    @Test
    fun `a tenant session cannot borrow the system identifier`() {
        assertFailsWith<IllegalArgumentException> { sessions.inTenant(HibernateTenantBridge.SYSTEM_TENANT_ID) { } }
    }

    private fun hashOf(issued: IssuedEnrollmentToken) = EnrollmentToken.parse(issued.reveal()).secret.hash()
}
