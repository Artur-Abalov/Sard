// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.onboarding

import dev.sard.server.TestcontainersConfiguration
import dev.sard.server.completeOnboarding
import dev.sard.server.extension.TenantResolver
import dev.sard.server.fleet.Agents
import dev.sard.server.pki.MovableClock
import dev.sard.server.runs.RUNS_NOW
import dev.sard.server.runs.RunsTestConfiguration
import dev.sard.server.runs.SourceDraft
import dev.sard.server.runs.Sources
import dev.sard.server.selfbackup.S3_REPOSITORY
import dev.sard.server.selfbackup.SelfBackupTenant
import dev.sard.server.selfbackup.SelfBackups
import dev.sard.server.selfbackup.SystemSourceLookup
import io.github.anschnapp.mutflow.MutFlow
import io.github.anschnapp.mutflow.junit.MutFlowTest
import org.junit.jupiter.api.Tag
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.jdbc.core.JdbcTemplate
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/** D8, Р22: what the guard of the REST API learns of the objects a path names, through the tenant's session. */
@Tag("mutflow-isolated")
@MutFlowTest(includeTargets = [TenantGuardTargets::class, SystemSourceLookup::class])
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class, RunsTestConfiguration::class)
class TenantGuardTargetsIntegrationTest(
    @Autowired private val selfBackups: SelfBackups,
    @Autowired private val systemSources: SystemSourceLookup,
    @Autowired private val agents: Agents,
    @Autowired private val sources: Sources,
    @Autowired private val clock: MovableClock,
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = SelfBackupTenant(jdbc)
    private val other = SelfBackupTenant(jdbc)
    private val targets = TenantGuardTargets(systemSources, agents, TenantResolver { tenant.id })

    @BeforeTest
    fun `two tenants with their built-in agents`() {
        clock.now = RUNS_NOW
        tenant.create()
        other.create()
    }

    @AfterTest
    fun `drop the tenants`() {
        tenant.drop()
        other.drop()
        completeOnboarding(jdbc)
    }

    private fun isBuiltinAgent(id: UUID) = MutFlow.underTest { targets.isBuiltinAgent(id) }

    private fun isSystemSource(id: UUID) = MutFlow.underTest { targets.isSystemSource(id) }

    @Test
    fun `A live built-in agent of the tenant is one, an ordinary, revoked, foreign or unknown agent is not`() {
        val ordinary = UUID.randomUUID().also { tenant.insertAgent(it, builtin = false) }

        assertTrue(isBuiltinAgent(tenant.agentId))
        assertFalse(isBuiltinAgent(ordinary))
        assertFalse(isBuiltinAgent(other.agentId))
        assertFalse(isBuiltinAgent(UUID.randomUUID()))

        tenant.revoke(tenant.agentId)

        assertFalse(isBuiltinAgent(tenant.agentId))
    }

    @Test
    fun `A live system source of the tenant is one, an ordinary, deleted, foreign or unknown source is not`() {
        val bound = selfBackups.bind(tenant.id, S3_REPOSITORY, confirmLocalStorage = false)
        val system = bound.sources.map { it.sourceId }
        val draft = SourceDraft("ordinary", tenant.agentId, "files", S3_REPOSITORY, """{"paths": ["/srv"]}""")
        val ordinary = sources.create(tenant.id, draft).id
        val foreign =
            selfBackups
                .bind(other.id, S3_REPOSITORY, confirmLocalStorage = false)
                .sources
                .first()
                .sourceId

        assertTrue(system.all(::isSystemSource))
        assertFalse(isSystemSource(ordinary))
        assertFalse(isSystemSource(foreign))
        assertFalse(isSystemSource(UUID.randomUUID()))

        jdbc.update("update sources set deleted_at = now() where id = ?", system.first())

        assertFalse(isSystemSource(system.first()))
    }
}
