// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.agents

import com.google.protobuf.UnknownFieldSet
import com.google.rpc.ErrorInfo
import dev.sard.proto.agent.v1.AgentServiceGrpc
import dev.sard.proto.agent.v1.ProbeRequest
import dev.sard.proto.agent.v1.ProbeResponse
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.Enrollment
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.fleet.Agents
import dev.sard.server.pki.CertificateAuthority
import io.grpc.Status
import io.grpc.StatusRuntimeException
import io.grpc.protobuf.StatusProto
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.grpc.test.autoconfigure.LocalGrpcServerPort
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.time.Instant
import java.util.concurrent.Executors
import kotlin.test.AfterTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

private val EMPTY = ProbeRequest.getDefaultInstance()
private const val CONCURRENT = 20
private const val SERIAL_OF = "select serial from agent_certificates where agent_id = ?"
private const val PLUGINS_OF = "select count(*) from agent_plugins where agent_id = ?"
private const val LAST_SEEN = "select last_seen_at from agents where id = ?"
private const val EXPIRE =
    "update agent_certificates set issued_at = now() - interval '2 seconds', " +
        "not_after = now() - interval '1 second' where agent_id = ?"

/**
 * A8d-3 (docs/specs/server/agent-probe.feature), the @grpc scenarios against a real server over mTLS:
 * Probe answers the server clock, passes the same certificate check as every AgentService call and
 * changes nothing in the database.
 */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.RANDOM_PORT,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class ProbeIntegrationTest(
    @Autowired private val ca: CertificateAuthority,
    @Autowired private val enrollment: Enrollment,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val agents: Agents,
    @LocalGrpcServerPort private val grpcPort: Int,
) {
    private val fixtures = AgentGrpcFixtures(ca, enrollment, tokens, jdbc, grpcPort)

    @AfterTest
    fun cleanup() = fixtures.close()

    private fun stub(agent: EnrolledAgent) = AgentServiceGrpc.newBlockingStub(fixtures.channel(agent))

    private fun serial(agent: EnrolledAgent): String {
        val id = agent.identity.agentId
        return jdbc.queryForObject(SERIAL_OF, String::class.java, id)!!
    }

    private fun refusal(block: () -> Unit): ErrorInfo {
        val error = runCatching(block).exceptionOrNull() as? StatusRuntimeException
        assertNotNull(error, "the call was expected to fail")
        assertEquals(Status.Code.UNAUTHENTICATED, error.status.code)
        assertEquals("agent certificate rejected", error.status.description)
        val info = StatusProto.fromStatusAndTrailers(error.status, error.trailers).detailsList.single()
        return info.unpack(ErrorInfo::class.java).also {
            assertEquals("sard.dev", it.domain)
            assertTrue(it.metadataMap.isEmpty())
        }
    }

    /** Every table of the schema, row by row, sorted: equal before and after means nothing was written. */
    private fun snapshot(): Map<String, List<String>> =
        jdbc
            .queryForList("select tablename from pg_tables where schemaname = 'public' order by 1", String::class.java)
            .filterNotNull()
            .associateWith { table ->
                jdbc
                    .queryForList("select to_jsonb(t)::text from \"$table\" t", String::class.java)
                    .filterNotNull()
                    .sorted()
            }

    @Test
    fun `an agent that never called Register gets the server time and stays unregistered`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val before = Instant.now()
        val answer = stub(agent).probe(EMPTY)
        val time = Instant.ofEpochSecond(answer.serverTime.seconds, answer.serverTime.nanos.toLong())
        assertTrue(Duration.between(before, time).abs() < Duration.ofSeconds(30), "$time")
        val version = "select agent_version from agents where id = ?"
        assertNull(jdbc.queryForObject(version, String::class.java, agent.identity.agentId))
        assertEquals(
            0,
            jdbc.queryForObject(PLUGINS_OF, Int::class.java, agent.identity.agentId),
        )
    }

    @Test
    fun `the answer holds field 1 only and no identifier of the agent`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val answer = stub(agent).probe(EMPTY)
        assertEquals(
            setOf(1),
            answer.allFields.keys
                .map { it.number }
                .toSet(),
        )
        assertTrue(answer.unknownFields.asMap().isEmpty())
        val text = answer.toByteArray().toString(Charsets.ISO_8859_1)
        assertFalse(agent.identity.agentId.toString() in text)
        assertFalse(agent.identity.tenantId.toString() in text)
    }

    @Test
    fun `a request with an unknown field from a newer agent is accepted`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val unknown =
            UnknownFieldSet
                .newBuilder()
                .addField(
                    99,
                    UnknownFieldSet.Field
                        .newBuilder()
                        .addVarint(1)
                        .build(),
                ).build()
        val request = ProbeRequest.newBuilder().setUnknownFields(unknown).build()
        assertTrue(stub(agent).probe(request).hasServerTime())
    }

    @Test
    fun `Probe of a live agent changes nothing in the database`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val before = snapshot()
        stub(agent).probe(EMPTY)
        assertEquals(before, snapshot())
    }

    @Test
    fun `Probe does not move the time of the last contact`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val id = agent.identity.agentId
        jdbc.update("update agents set last_seen_at = '2026-10-14T11:00:00Z' where id = ?", id)
        val before = jdbc.queryForObject(LAST_SEEN, java.sql.Timestamp::class.java, id)
        stub(agent).probe(EMPTY)
        assertEquals(before, jdbc.queryForObject(LAST_SEEN, java.sql.Timestamp::class.java, id))
    }

    @Test
    fun `20 simultaneous Probes are all accepted and change nothing`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        val before = snapshot()
        val pool = Executors.newFixedThreadPool(CONCURRENT)
        try {
            val calls = (1..CONCURRENT).map { pool.submit<ProbeResponse> { stub(agent).probe(EMPTY) } }
            calls.forEach { assertTrue(it.get().hasServerTime()) }
        } finally {
            pool.shutdownNow()
        }
        assertEquals(before, snapshot())
    }

    @Test
    fun `a revoked certificate is CERT_REVOKED and the refusal changes nothing`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        stub(agent).probe(EMPTY)
        jdbc.update("update agent_certificates set revoked_at = now() where agent_id = ?", agent.identity.agentId)
        val before = snapshot()
        val info = refusal { stub(agent).probe(EMPTY) }
        assertEquals("CERT_REVOKED", info.reason)
        assertFalse(serial(agent) in info.toString())
        assertEquals(before, snapshot())
    }

    @Test
    fun `an agent revoked with an unrevoked certificate is AGENT_REVOKED`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        jdbc.update("update agents set revoked_at = now() where id = ?", agent.identity.agentId)
        assertEquals("AGENT_REVOKED", refusal { stub(agent).probe(EMPTY) }.reason)
    }

    @Test
    fun `an agent revoked in the console is CERT_REVOKED`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        agents.revoke(agent.identity.tenantId, agent.identity.agentId, null)
        assertEquals("CERT_REVOKED", refusal { stub(agent).probe(EMPTY) }.reason)
    }

    @Test
    fun `a certificate at the not_after of its record is CERT_EXPIRED`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        jdbc.update(
            EXPIRE,
            agent.identity.agentId,
        )
        assertEquals("CERT_EXPIRED", refusal { stub(agent).probe(EMPTY) }.reason)
    }

    @Test
    fun `a certificate without a record is CERT_UNKNOWN`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        jdbc.update("delete from agent_certificates where agent_id = ?", agent.identity.agentId)
        assertEquals("CERT_UNKNOWN", refusal { stub(agent).probe(EMPTY) }.reason)
    }

    @Test
    fun `a revoked certificate of a revoked agent is CERT_REVOKED, not AGENT_REVOKED`() {
        val agent = fixtures.enrolled(fixtures.tenant())
        jdbc.update("update agents set revoked_at = now() where id = ?", agent.identity.agentId)
        jdbc.update("update agent_certificates set revoked_at = now() where agent_id = ?", agent.identity.agentId)
        assertEquals("CERT_REVOKED", refusal { stub(agent).probe(EMPTY) }.reason)
    }

    @Test
    fun `the unauthenticated services stay Enrollment and Health`() {
        assertEquals(2, UNAUTHENTICATED_SERVICES.size)
        assertFalse(AgentServiceGrpc.SERVICE_NAME in UNAUTHENTICATED_SERVICES)
    }

    @Test
    fun `an agent of another tenant is not touched by a Probe`() {
        val a = fixtures.enrolled(fixtures.tenant())
        fixtures.enrolled(fixtures.tenant())
        val before = snapshot()
        stub(a).probe(EMPTY)
        assertEquals(before, snapshot())
    }
}
