// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.extension.TenantResolver
import dev.sard.server.pki.CertificateAuthority
import dev.sard.server.runs.AgentRevoked
import dev.sard.server.runs.UnknownPlugin
import dev.sard.server.runs.UnknownRepository
import dev.sard.server.selfbackup.CommandBuild
import dev.sard.server.selfbackup.PlanItem
import dev.sard.server.selfbackup.RepositoryCommands
import dev.sard.server.selfbackup.SelfAgentMissing
import dev.sard.server.selfbackup.SelfBackupNotConfigured
import dev.sard.server.selfbackup.SelfBackupRun
import dev.sard.server.selfbackup.SelfBackupView
import dev.sard.server.selfbackup.SelfBackups
import dev.sard.server.selfbackup.SystemSource
import org.springframework.boot.info.BuildProperties
import org.springframework.stereotype.Component
import java.time.Clock

/** The self-backup endpoints over [SelfBackups], in the tenant of the session. */
@Component
class SelfBackupApiImpl(
    private val selfBackups: SelfBackups,
    private val tenants: TenantResolver,
    private val onboarding: OnboardingApi,
    private val ca: CertificateAuthority,
    private val build: BuildProperties,
    private val clock: Clock,
) : SelfBackupApi {
    override fun getSelfBackup(): SelfBackup = selfBackupOf(selfBackups.get(tenants.currentTenantId()))

    override fun bindSelfBackupRepository(input: SelfBackupRepositoryInput): SelfBackup {
        val tenant = tenants.currentTenantId()
        return selfBackupOf(selfBackups.bind(tenant, input.repositoryName, input.confirmLocalStorage, input.location))
    }

    override fun confirmKeys(
        input: KeysConfirmationInput,
        clientAddress: String,
    ) {
        if (input.copiedOutsideServer != true) throw RequestInvalid("copiedOutsideServer", "must be true")
        onboarding.confirmKeys(clientAddress)
    }

    override fun repositoryCommand(query: RepositoryCommandQuery): RepositoryCommand {
        val view = selfBackups.get(tenants.currentTenantId())
        val name = RepositoryCommands.freeName(view.repositories.map { it.name })
        val built = RepositoryCommands.build(name, query.asMap())
        if (built is CommandBuild.Rejected) {
            throw RequestRefused(ErrorCode.VALIDATION_FAILED, built.fields.map { FieldError(it, REJECTED_PARAMETER) })
        }
        if (view.agentId == null) throw SelfAgentMissing()
        val command = built as CommandBuild.Built
        return RepositoryCommand(name, command.address, command.commands)
    }

    override fun recoveryCard(): RecoveryCard {
        val view = selfBackups.get(tenants.currentTenantId())
        val repository = view.repository
        if (!view.configured || repository == null) throw SelfBackupNotConfigured()
        val version = build.version ?: UNKNOWN_VERSION
        return RecoveryCard(
            repository.name,
            repository.backend,
            view.location,
            repository.repositoryId,
            ca.fingerprint().hex,
            version,
            clock.instant(),
            recoveryProcedureUrl(version),
        )
    }

    /** As POST /sources/{id}/runs: a run that cannot start for its agent is a conflict, not a bad request. */
    override fun startSelfBackup(): SelfBackupRuns =
        try {
            SelfBackupRuns(selfBackups.runNow(tenants.currentTenantId()).map(::runOf))
        } catch (_: AgentRevoked) {
            throw RunRefused(ErrorCode.AGENT_REVOKED)
        } catch (_: UnknownPlugin) {
            throw RunRefused(ErrorCode.UNKNOWN_PLUGIN)
        } catch (_: UnknownRepository) {
            throw RunRefused(ErrorCode.UNKNOWN_REPOSITORY)
        }

    private fun runOf(run: SelfBackupRun) =
        SelfBackupRunStarted(SystemSourceRole.valueOf(run.role.name), run.sourceId, run.runId, run.started)

    private fun selfBackupOf(view: SelfBackupView) =
        SelfBackup(
            view.configured,
            view.agentId,
            view.repository?.let { SelfBackupRepository(it.name, it.backend, it.repositoryId, it.local) },
            view.boundAt,
            view.sources.map(::sourceOf),
            SelfBackupStatus.valueOf(view.health.name),
            view.warnings.map { SelfBackupWarningCode.valueOf(it.name) },
            view.overdueSince,
            view.repositories.map { SelfBackupRepository(it.name, it.backend, it.repositoryId, it.local) },
            view.plan.map(::planItemOf),
            view.firstBackupDone,
            view.keysConfirmedAt,
            view.passwordRevealCommand,
        )

    private fun planItemOf(item: PlanItem) =
        SelfBackupPlanItem(
            SystemSourceRole.valueOf(item.role.name),
            item.name,
            item.plugin,
            item.includes,
            item.excludes,
        )

    private fun sourceOf(source: SystemSource) =
        SelfBackupSource(
            SystemSourceRole.valueOf(source.role.name),
            source.sourceId,
            source.agentId,
            source.repositoryName,
            source.lastSuccessAt,
            source.lastFailureAt,
            source.lastRun?.let { SelfBackupLastRun(it.id, RunStatus.valueOf(it.status.name), it.finishedAt) },
        )
}

private const val REJECTED_PARAMETER = "is missing or not acceptable"
private const val UNKNOWN_VERSION = "unknown"
