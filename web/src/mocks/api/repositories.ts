// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { http } from '../http'
import { noSession, PROBLEM } from '../problems'
import { state } from '../state'

type Schemas = components['schemas']

interface Holder {
  agent: Schemas['AgentDetails']
  repository: Schemas['AgentRepository']
}

/** The first answer that is not "unknown"; the server takes the latest Register, the mock the first holder. */
function flagOf(holders: Holder[]): Schemas['RecoveryKeyFlag'] {
  return (
    holders.find((h) => h.repository.recoveryKey !== 'unknown')?.repository.recoveryKey ?? 'unknown'
  )
}

function itemOf(repositoryId: string, holders: Holder[]): Schemas['RepositoryInfo'] {
  const recoveryKey = flagOf(holders)
  const source = holders.find((h) => h.agent.role === 'source')
  const command =
    recoveryKey === 'absent' && source !== undefined
      ? `sudo sard-agent repo key add ${source.repository.name} --for recovery`
      : null
  return {
    repositoryId,
    recoveryKey,
    reportedAt: null,
    reportedByAgentId: null,
    recoveryKeyCommand: command,
    holders: holders.map(({ agent, repository }) => ({
      agentId: agent.id,
      hostname: agent.hostname,
      role: agent.role,
      repositoryName: repository.name,
    })),
  }
}

function listOf(agents: Schemas['AgentDetails'][]): Schemas['RepositoryList'] {
  const byId = new Map<string, Holder[]>()
  for (const agent of agents.filter((a) => a.revokedAt === null)) {
    for (const repository of agent.repositories) {
      if (repository.repositoryId === null) continue
      const holders = byId.get(repository.repositoryId) ?? []
      byId.set(repository.repositoryId, [...holders, { agent, repository }])
    }
  }
  const items = [...byId.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([id, holders]) => itemOf(id, holders))
  return { items, withoutRecoveryKey: items.filter((i) => i.recoveryKey === 'absent').length }
}

// The repositories the agents report and the flag of their recovery key (V3, КС1).
export const repositoryHandlers = [
  http.get('/api/v1/repositories', ({ response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    return response(200).json(listOf(state.agents))
  }),
]
