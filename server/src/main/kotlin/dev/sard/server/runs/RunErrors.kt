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

/** No such agent in the tenant, or it is revoked (422 unknown_agent). */
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
