// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../api/schema'

type Schemas = components['schemas']

export const MIN_TTL_SECONDS = 300
export const MAX_TTL_SECONDS = 604_800
export const MAX_LABEL_LENGTH = 200

function rejected(
  code: Schemas['ErrorCode'],
  field: string,
  message: string,
): Schemas['ValidationProblem'] {
  return {
    type: 'about:blank',
    title: 'Unprocessable Content',
    status: 422,
    detail: message,
    code,
    errors: [{ field, message }],
  }
}

/** The server's checks of a source that the mock can make: its agent, plugin and repository exist. */
export function validateSource(
  source: Schemas['SourceInput'],
  agents: Schemas['AgentDetails'][],
): Schemas['ValidationProblem'] | null {
  const agent = agents.find((a) => a.id === source.agentId)
  if (agent === undefined) return rejected('unknown_agent', 'agentId', 'no such agent')
  if (agent.revokedAt !== null) return rejected('agent_revoked', 'agentId', 'the agent is revoked')
  if (!agent.plugins.some((p) => p.name === source.plugin)) {
    return rejected('unknown_plugin', 'plugin', `the agent has no plugin ${source.plugin}`)
  }
  if (!agent.repositories.some((r) => r.name === source.repositoryName)) {
    return rejected(
      'unknown_repository',
      'repositoryName',
      `repository ${source.repositoryName} is not in the agent's last Register`,
    )
  }
  return null
}

/** Token lifetime: 5 minutes to 7 days, or omitted. */
export function validateTtl(
  ttlSeconds: number | null | undefined,
): Schemas['ValidationProblem'] | null {
  if (ttlSeconds === null || ttlSeconds === undefined) return null
  if (ttlSeconds >= MIN_TTL_SECONDS && ttlSeconds <= MAX_TTL_SECONDS) return null
  return rejected(
    'validation_failed',
    'ttlSeconds',
    `from ${MIN_TTL_SECONDS} to ${MAX_TTL_SECONDS} seconds`,
  )
}

/** A token's label: up to 200 characters, or omitted. */
export function validateLabel(
  label: string | null | undefined,
): Schemas['ValidationProblem'] | null {
  if (label === null || label === undefined || label.length <= MAX_LABEL_LENGTH) return null
  return rejected('validation_failed', 'label', `at most ${MAX_LABEL_LENGTH} characters`)
}
