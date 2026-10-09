// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import dev.sard.server.TestcontainersConfiguration
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.dao.DataIntegrityViolationException
import org.springframework.jdbc.core.JdbcTemplate
import java.sql.Timestamp
import java.util.UUID
import kotlin.test.AfterTest
import kotlin.test.BeforeTest
import kotlin.test.Test
import kotlin.test.assertContains
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/** The database itself refuses what the S6a schema forbids, whatever the code does (ADR 0013, ADR 0022). */
@SpringBootTest(
    webEnvironment = SpringBootTest.WebEnvironment.NONE,
    properties = ["spring.grpc.server.port=0"],
)
@Import(TestcontainersConfiguration::class)
class RunSchemaIntegrationTest(
    @Autowired private val jdbc: JdbcTemplate,
) {
    private val tenant = RunsTenant(jdbc)
    private val sourceId = UUID.randomUUID()
    private val at = Timestamp.from(RUNS_NOW)

    @BeforeTest
    fun `a tenant with a source`() {
        tenant.create()
        jdbc.update(
            """
            insert into sources (id, tenant_id, agent_id, name, plugin, config, repository_name, created_at, updated_at)
            values (?, ?, ?, 'prod-db', 'postgresql', '{}'::jsonb, 'main', ?, ?)
            """.trimIndent(),
            sourceId,
            tenant.id,
            tenant.agentId,
            at,
            at,
        )
    }

    @AfterTest
    fun `drop the tenant`() {
        tenant.drop()
    }

    private fun insertRun(
        status: String,
        finished: Boolean = status !in setOf("queued", "dispatched", "running"),
        workflow: UUID? = null,
        trigger: String = "manual",
    ): UUID {
        val id = UUID.randomUUID()
        jdbc.update(
            """
            insert into runs (id, tenant_id, source_id, workflow_id, trigger, status, queued_at, finished_at)
            values (?, ?, ?, ?, ?, ?, ?, ?)
            """.trimIndent(),
            id,
            tenant.id,
            sourceId,
            workflow,
            trigger,
            status,
            at,
            if (finished) at else null,
        )
        return id
    }

    private fun insertStep(
        runId: UUID,
        status: String = "queued",
        dispatched: Boolean = false,
        action: String = "backup",
        source: UUID? = sourceId,
        repository: String? = "main",
    ) {
        jdbc.update(
            """
            insert into run_steps (id, tenant_id, run_id, ordinal, agent_id, source_id, plugin, action,
                                   repository_name, config, status, queued_at, dispatched_at)
            values (?, ?, ?, 0, ?, ?, 'postgresql', ?, ?, '{}'::jsonb, ?, ?, ?)
            """.trimIndent(),
            UUID.randomUUID(),
            tenant.id,
            runId,
            tenant.agentId,
            source,
            action,
            repository,
            status,
            at,
            if (dispatched) at else null,
        )
    }

    private fun refused(
        constraint: String,
        block: () -> Unit,
    ) {
        val error = assertFailsWith<DataIntegrityViolationException> { block() }
        assertContains(error.mostSpecificCause.message.orEmpty(), constraint)
    }

    @Test
    fun `a second active run of one source is refused by the D6 index, whatever its status`() {
        insertRun("queued")
        for (status in listOf("queued", "dispatched", "running")) {
            refused("runs_active_source_key") { insertRun(status) }
        }
    }

    @Test
    fun `finished runs of a source never block a new one`() {
        for (status in listOf("succeeded", "failed", "cancelled")) insertRun(status)
        insertRun("queued")
        assertEquals(4, tenant.count("runs"))
    }

    @Test
    fun `an active run has no finish time and a finished one has it`() {
        refused("runs_finished_check") { insertRun("running", finished = true) }
        refused("runs_finished_check") { insertRun("failed", finished = false) }
    }

    @Test
    fun `a verification run needs a workflow, manual and scheduled runs go without one`() {
        // ADR 0022 as amended by F3a: a schedule fires its source, not a workflow.
        refused("runs_workflow_check") { insertRun("succeeded", trigger = "verification") }
        insertRun("succeeded", trigger = "schedule")
        insertRun("succeeded", trigger = "catch_up")
        refused("runs_trigger_check") { insertRun("succeeded", trigger = "cron") }
    }

    @Test
    fun `a run status outside the contract is refused`() {
        refused("runs_status_check") { insertRun("lost") }
    }

    @Test
    fun `a queued step has no dispatch time and every other one has it`() {
        val run = insertRun("queued")
        refused("run_steps_dispatched_check") { insertStep(run, "queued", dispatched = true) }
        refused("run_steps_dispatched_check") { insertStep(run, "dispatched", dispatched = false) }
    }

    @Test
    fun `a backup step names its source and repository, a script step neither`() {
        val run = insertRun("queued")
        refused("run_steps_source_check") { insertStep(run, action = "backup", source = null) }
        refused("run_steps_repository_check") { insertStep(run, action = "backup", repository = null) }
        refused("run_steps_source_check") { insertStep(run, action = "run", repository = null) }
    }

    @Test
    fun `a source of a deleted name can be created again, a live name only once`() {
        val insert = { id: UUID ->
            jdbc.update(
                """
                insert into sources (id, tenant_id, agent_id, name, plugin, config, repository_name,
                                     created_at, updated_at)
                values (?, ?, ?, 'prod-db', 'postgresql', '{}'::jsonb, 'main', ?, ?)
                """.trimIndent(),
                id,
                tenant.id,
                tenant.agentId,
                at,
                at,
            )
        }
        refused("sources_tenant_id_name_key") { insert(UUID.randomUUID()) }
        jdbc.update("update sources set deleted_at = ? where id = ?", at, sourceId)
        insert(UUID.randomUUID())
        assertEquals(2, tenant.count("sources"))
    }

    @Test
    fun `a source config is a JSON object`() {
        refused("sources_config_check") {
            jdbc.update("update sources set config = '[1]'::jsonb where id = ?", sourceId)
        }
    }
}
