// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.runs

import java.util.UUID

/** Why the source or run service refused; S8b maps each one to its problem (S8a contract). */
sealed class RunsException(
    message: String,
) : RuntimeException(message)

/** No live source with this id in the caller's tenant; a deleted source counts as absent (404). */
class SourceNotFound(
    val sourceId: UUID,
) : RunsException("no source $sourceId")

/** Another live source of the tenant has this name (422 validation_failed on `name`). */
class SourceNameTaken(
    val name: String,
) : RunsException("a source named $name exists")

/** No such agent in the tenant (422 unknown_agent). */
class UnknownAgent(
    val agentId: UUID,
) : RunsException("no agent $agentId")

/** The agent's last Register did not offer this plugin (422 unknown_plugin). */
class UnknownPlugin(
    val plugin: String,
) : RunsException("the agent offers no plugin $plugin")

/** The agent's last Register did not list this repository (422 unknown_repository). */
class UnknownRepository(
    val repositoryName: String,
) : RunsException("repository $repositoryName is not in the agent's last Register")

/** The source has an active run (D6, 409 run_active). */
class RunActive(
    val activeRunId: UUID,
) : RunsException("run $activeRunId is active")

/** The agent is revoked (422 agent_revoked when a source names it, 409 when a run of its source starts). */
class AgentRevoked(
    val agentId: UUID,
) : RunsException("agent $agentId is revoked")

/** One thing wrong with a source's config: [field] is `config` plus the JSON Pointer of the value. */
data class ConfigViolation(
    val field: String,
    val message: String,
)

/** The config does not fit the plugin's schema or names an unknown secret (422 invalid_config). */
class InvalidConfig(
    val violations: List<ConfigViolation>,
) : RunsException("the config has ${violations.size} violations")

/** The source is one the server keeps itself (F6): only its schedule changes through the API (409 system_source). */
class SystemSourceProtected(
    val sourceId: UUID,
) : RunsException("source $sourceId is a system source")
