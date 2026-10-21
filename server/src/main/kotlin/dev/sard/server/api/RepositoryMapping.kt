// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package dev.sard.server.api

import dev.sard.server.fleet.AgentRepositoryView
import dev.sard.server.fleet.VerifierSetupItem

/** A repository of an agent as the card and the list show it. */
internal fun AgentRepositoryView.toApi(): AgentRepository =
    AgentRepository(name, backend, repositoryId, cryptoProvider, recoveryKey.toApi())

/** What a verifier is offered, as the card shows it. */
internal fun VerifierSetupItem.toApi() =
    VerifierSetupEntry(repositoryId, sourceAgentId, sourceHostname, repositoryName, keyAddCommand, repoAddTemplate)
