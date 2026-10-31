// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestAdmin
import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.enrollment.EnrollmentTokenValidationException
import dev.sard.server.enrollment.EnrollmentTokens
import dev.sard.server.events.ActorSource
import dev.sard.server.events.AfterCommit
import dev.sard.server.events.SardEventPublisher
import dev.sard.server.events.SettingsEvents
import dev.sard.server.extension.EventLog
import dev.sard.server.extension.RecordingConfiguration
import dev.sard.server.extension.SardEvent
import dev.sard.server.extension.SettingsChanged
import dev.sard.server.extension.Subject
import dev.sard.server.fleet.AgentRevocation
import dev.sard.server.fleet.Agents
import dev.sard.server.fleet.SELF_AGENT_CONFIRMATION
import dev.sard.server.persistence.TenantSessions
import dev.sard.server.pki.MovableClock
import dev.sard.server.role.AgentRole
import dev.sard.server.scheduler.ScheduleDraft
import dev.sard.server.scheduler.Schedules
import dev.sard.server.verify.VerificationSchedule
import dev.sard.server.verify.VerificationSwitch
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.Tag
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.time.Duration
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

private val ACTOR = Subject("administrator", "admin")
private const val SPELLED = """{"password": {"secret": "pg-prod"}, "database": "app"}"""
private const val ANOTHER = """{"database": "other", "password": {"secret": "pg-prod"}}"""
private const val ERASED = "delete from verification_schedule"

/**
 * Mutation tests of the settings events decided in the domain (CORE1 Р28): each writer of a setting asks for the
 * event in its own transaction when what it wrote differs from what was there, and the event goes out when the
 * transaction has committed, not at all when it rolls back.
 */
@Tag("mutflow-isolated")
@MutFlowTest(
    includeTargets = [
        TimeoutSettings::class,
        Sources::class,
        Schedules::class,
        EnrollmentTokens::class,
        Agents::class,
        VerificationSwitch::class,
        VerificationSchedule::class,
        SettingsEvents::class,
        AfterCommit::class,
    ],
)
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class, RecordingConfiguration::class)
class SettingsEventsDomainMutationTest(
    @Autowired private val settings: TimeoutSettings,
    @Autowired private val sources: Sources,
    @Autowired private val schedules: Schedules,
    @Autowired private val tokens: EnrollmentTokens,
    @Autowired private val agents: Agents,
    @Autowired private val verificationSwitch: VerificationSwitch,
    @Autowired private val verificationSchedule: VerificationSchedule,
    @Autowired private val sessions: TenantSessions,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
    @Autowired private val log: EventLog,
) {
    private val tenant = RunsTenant(jdbc)

    @BeforeTest
    fun `a tenant with an agent, no events`() {
        clock.now = RUNS_NOW
        tenant.create()
        log.clear()
    }

    @AfterTest
    fun `drop the tenant`() {
        jdbc.update("delete from schedules where tenant_id = ?", tenant.id)
        jdbc.update("delete from enrollment_tokens where tenant_id = ?", tenant.id)
        tenant.drop()
    }

    private fun heard(setting: String): List<SettingsChanged> {
        val all = log.settings(setting)
        return all.filter { it.tenantId == tenant.id }
    }

    private fun turn(
        source: UUID,
        enabled: Boolean,
    ) = sessions.inTenant(tenant.id) { session ->
        MutFlow.underTest { verificationSwitch.set(session, tenant.id, source, enabled) }
    }

    private fun source(name: String = "prod-db"): SourceView = sources.create(tenant.id, tenant.draft(name))

    @Test
    fun `The timeouts of a tenant publish run_defaults only when they differ`() {
        val saved = MutFlow.underTest { settings.set(tenant.id, TimeoutDefaults(480, 600)) }
        assertEquals(TimeoutDefaults(480, 600), saved)
        val first = heard("run_defaults").single()
        assertEquals(null, first.objectId)
        assertEquals(RUNS_NOW, first.occurredAt)

        MutFlow.underTest { settings.set(tenant.id, TimeoutDefaults(480, 600)) }
        assertEquals(1, heard("run_defaults").size)

        MutFlow.underTest { settings.set(tenant.id, TimeoutDefaults(480, 601)) }
        assertEquals(2, heard("run_defaults").size)

        MutFlow.underTest { settings.set(tenant.id, TimeoutDefaults(481, 601)) }
        assertEquals(3, heard("run_defaults").size)
    }

    @Test
    fun `The initial timeouts saved by a tenant that saved nothing change nothing`() {
        MutFlow.underTest { settings.set(tenant.id, TimeoutDefaults.INITIAL) }

        assertEquals(emptyList(), heard("run_defaults"))
        assertEquals(TimeoutDefaults.INITIAL, settings.get(tenant.id))
    }

    @Test
    fun `A source created or deleted publishes source_created and source_deleted with its id`() {
        val created = MutFlow.underTest { sources.create(tenant.id, tenant.draft()) }

        assertEquals(created.id, heard("source_created").single().objectId)
        assertEquals(emptyList(), heard("source_deleted"))

        MutFlow.underTest { sources.delete(tenant.id, created.id) }

        assertEquals(created.id, heard("source_deleted").single().objectId)
        assertEquals(1, heard("source_created").size)
    }

    @Test
    fun `A source replaced by what it already is publishes nothing, whatever the spelling of its config`() {
        val created = sources.create(tenant.id, tenant.draft(config = SPELLED))
        log.clear()

        MutFlow.underTest { sources.replace(tenant.id, created.id, tenant.draft(config = SPELLED)) }
        MutFlow.underTest { sources.replace(tenant.id, created.id, tenant.draft(config = SPELLED.replace(" ", ""))) }
        val keeping = tenant.draft(config = SPELLED).copy(keepBackupTimeout = true)
        MutFlow.underTest { sources.replace(tenant.id, created.id, keeping) }

        assertEquals(emptyList(), heard("source_updated"))
    }

    @Test
    fun `A source replaced by something else publishes source_updated for each field that differs`() {
        val other = UUID.randomUUID()
        tenant.insertAgent(other)
        jdbc.update(
            "insert into agent_plugins (tenant_id, agent_id, name, version, config_schema, actions) " +
                "values (?, ?, 'files', '0.1.0', '{}'::jsonb, array['backup','restore'])",
            tenant.id,
            tenant.agentId,
        )
        jdbc.update(
            "insert into agent_repositories (tenant_id, agent_id, name, backend) values (?, ?, 'second', 'local')",
            tenant.id,
            tenant.agentId,
        )
        val base = tenant.draft(config = SPELLED)
        val created = sources.create(tenant.id, base)
        val replacements =
            listOf(
                tenant.draft(name = "renamed", config = SPELLED),
                tenant.draft(agent = other, config = SPELLED),
                tenant.draft(plugin = "files", config = SPELLED),
                tenant.draft(repository = "second", config = SPELLED),
                tenant.draft(config = ANOTHER),
                tenant.draft(config = SPELLED).copy(backupTimeoutMinutes = 30),
                tenant.draft(config = SPELLED).copy(restoreTimeoutMinutes = 40),
            )
        for (replacement in replacements) {
            log.clear()

            MutFlow.underTest { sources.replace(tenant.id, created.id, replacement) }

            assertEquals(created.id, heard("source_updated").singleOrNull()?.objectId, replacement.toString())
            // Back to the starting point for the next field.
            sources.replace(tenant.id, created.id, base.copy(backupTimeoutMinutes = null, restoreTimeoutMinutes = null))
        }
    }

    @Test
    fun `A schedule publishes source_schedule when it is new or any of its parts differs`() {
        val source = source().id
        val draft = ScheduleDraft("0 4 * * *", "UTC", true, false)
        val changes =
            listOf(
                draft.copy(cron = "0 5 * * *"),
                draft.copy(cron = "0 5 * * *", timezone = "Europe/Moscow"),
                draft.copy(cron = "0 5 * * *", timezone = "Europe/Moscow", enabled = false),
                draft.copy(cron = "0 5 * * *", timezone = "Europe/Moscow", enabled = false, notifyOnSuccess = true),
            )

        MutFlow.underTest { schedules.set(tenant.id, source, draft) }
        assertEquals(source, heard("source_schedule").single().objectId)

        MutFlow.underTest { schedules.set(tenant.id, source, draft) }
        assertEquals(1, heard("source_schedule").size)

        changes.forEachIndexed { index, change ->
            MutFlow.underTest { schedules.set(tenant.id, source, change) }
            assertEquals(index + 2, heard("source_schedule").size, change.toString())
            MutFlow.underTest { schedules.set(tenant.id, source, change) }
            assertEquals(index + 2, heard("source_schedule").size, change.toString())
        }
    }

    @Test
    fun `A schedule that notifies only on success publishes when that alone is changed`() {
        val source = source().id
        val draft = ScheduleDraft("0 4 * * *", "UTC", true, false)
        schedules.set(tenant.id, source, draft)
        log.clear()

        MutFlow.underTest { schedules.set(tenant.id, source, draft.copy(notifyOnSuccess = true)) }

        assertEquals(source, heard("source_schedule").single().objectId)
    }

    @Test
    fun `A token created or revoked publishes token_created and token_revoked once, and a refused revoke nothing`() {
        val issued = MutFlow.underTest { tokens.create(tenant.id, role = AgentRole.SOURCE) }
        assertEquals(issued.id, heard("token_created").single().objectId)
        assertEquals(emptyList(), heard("token_revoked"))

        MutFlow.underTest { tokens.revoke(tenant.id, issued.id, RUNS_NOW) }
        MutFlow.underTest { tokens.revoke(tenant.id, issued.id, RUNS_NOW) }

        assertEquals(issued.id, heard("token_revoked").single().objectId)
        assertEquals(1, heard("token_created").size)

        val expired = tokens.create(tenant.id, role = AgentRole.SOURCE)
        log.clear()
        MutFlow.underTest { tokens.revoke(tenant.id, expired.id, RUNS_NOW + Duration.ofDays(30)) }
        MutFlow.underTest { tokens.revoke(tenant.id, UUID.randomUUID(), RUNS_NOW) }
        assertEquals(emptyList(), heard("token_revoked"))
    }

    @Test
    fun `A token out of range is refused, one at the edge is issued, and a refusal publishes nothing`() {
        val edges = listOf(Duration.ofMinutes(5), Duration.ofDays(7))
        for (ttl in edges) MutFlow.underTest { tokens.create(tenant.id, ttl, role = AgentRole.SOURCE) }
        val refused = listOf(Duration.ofMinutes(5).minusSeconds(1), Duration.ofDays(7).plusSeconds(1))
        for (ttl in refused) {
            assertFailsWith<EnrollmentTokenValidationException> {
                MutFlow.underTest { tokens.create(tenant.id, ttl, role = AgentRole.SOURCE) }
            }
        }
        MutFlow.underTest { tokens.create(tenant.id, Duration.ofHours(1), "x".repeat(200), AgentRole.SOURCE) }
        assertFailsWith<EnrollmentTokenValidationException> {
            MutFlow.underTest { tokens.create(tenant.id, Duration.ofHours(1), "x".repeat(201), AgentRole.SOURCE) }
        }

        assertEquals(3, heard("token_created").size)
    }

    @Test
    fun `A built-in token is internal and publishes nothing`() {
        MutFlow.underTest { tokens.replaceBuiltin(tenant.id) }
        MutFlow.underTest { tokens.replaceBuiltin(tenant.id) }

        assertEquals(emptyList(), log.settings("token_created").filter { it.tenantId == tenant.id })
        assertEquals(emptyList(), heard("token_revoked"))
    }

    @Test
    fun `An agent revoked publishes agent_revoked once, a repeat and an unknown agent nothing`() {
        MutFlow.underTest { agents.revoke(tenant.id, tenant.agentId, null) }
        MutFlow.underTest { agents.revoke(tenant.id, tenant.agentId, null) }
        MutFlow.underTest { agents.revoke(tenant.id, UUID.randomUUID(), null) }

        assertEquals(tenant.agentId, heard("agent_revoked").single().objectId)
    }

    @Test
    fun `A live built-in agent is revoked only when confirmed, and a refusal publishes nothing`() {
        jdbc.update("update agents set builtin = true where id = ?", tenant.agentId)

        val refused = MutFlow.underTest { agents.revoke(tenant.id, tenant.agentId, null) }
        assertEquals(AgentRevocation.ConfirmationRequired, refused)
        assertEquals(emptyList(), heard("agent_revoked"))

        MutFlow.underTest { agents.revoke(tenant.id, tenant.agentId, SELF_AGENT_CONFIRMATION) }
        assertEquals(tenant.agentId, heard("agent_revoked").single().objectId)
    }

    @Test
    fun `The verification of a source publishes source_verification when it is turned on or off, not when it stays`() {
        val source = source().id

        turn(source, true)
        assertEquals(emptyList(), heard("source_verification"))

        turn(source, false)
        turn(source, false)
        assertEquals(source, heard("source_verification").single().objectId)

        turn(source, true)
        assertEquals(2, heard("source_verification").size)
    }

    @Test
    fun `The verification of a source that is not there is not found and publishes nothing`() {
        val unknown = UUID.randomUUID()

        assertFailsWith<SourceNotFound> { turn(unknown, false) }

        assertEquals(emptyList(), heard("source_verification"))
    }

    private fun setCycle(
        cron: String,
        zone: String,
        stale: Int?,
    ) = sessions.inTenant(tenant.id) { session ->
        MutFlow.underTest { verificationSchedule.set(session, tenant.id, cron, zone, stale, RUNS_NOW) }
    }

    @Test
    fun `The cycle publishes verification_schedule when cron, zone or threshold differs, not when it is set again`() {
        TestAdmin.of(jdbc).update(ERASED)
        setCycle("15 3 * * *", "UTC", 3)
        assertEquals(1, heard("verification_schedule").size)
        assertEquals(null, heard("verification_schedule").single().objectId)

        setCycle("15 3 * * *", "UTC", 3)
        setCycle("15 3 * * *", "UTC", null)
        assertEquals(1, heard("verification_schedule").size)

        setCycle("16 3 * * *", "UTC", 3)
        assertEquals(2, heard("verification_schedule").size)
        setCycle("16 3 * * *", "Europe/Moscow", 3)
        assertEquals(3, heard("verification_schedule").size)
        setCycle("16 3 * * *", "Europe/Moscow", 4)
        assertEquals(4, heard("verification_schedule").size)
        TestAdmin.of(jdbc).update(ERASED)
    }

    @Test
    fun `An event asked for in a transaction goes out with its fields after the commit, not after a rollback`() {
        val published = mutableListOf<SardEvent>()
        val publisher = SardEventPublisher { published += it as SardEvent }
        val events = SettingsEvents(publisher, ActorSource { ACTOR }, clock) { tenant.id }
        val id = UUID.randomUUID()

        assertFailsWith<IllegalStateException> {
            sessions.inTenant(tenant.id) { session ->
                MutFlow.underTest { events.changed(session, tenant.id, "source_created", id) }
                assertEquals(emptyList(), published)
                error("rolled back")
            }
        }
        assertEquals(emptyList(), published)

        sessions.inTenant(tenant.id) { session ->
            MutFlow.underTest { events.changed(session, tenant.id, "source_created", id) }
            assertEquals(emptyList(), published)
        }

        assertEquals(listOf<SardEvent>(SettingsChanged(RUNS_NOW, tenant.id, ACTOR, "source_created", id)), published)
    }

    @Test
    fun `An event without an object names none`() {
        val published = mutableListOf<SardEvent>()
        val publisher = SardEventPublisher { published += it as SardEvent }
        val events = SettingsEvents(publisher, ActorSource.NONE, clock) { tenant.id }

        sessions.inTenant(tenant.id) { session ->
            MutFlow.underTest { events.changed(session, tenant.id, "run_defaults") }
        }

        assertEquals(listOf<SardEvent>(SettingsChanged(RUNS_NOW, tenant.id, null, "run_defaults", null)), published)
    }
}
