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

const CRON_FIELDS = 5
// The server refuses a normalised cron longer than this (F3b, К0), without echoing it.
export const MAX_CRON_LENGTH = 200

/** A cron as the server stores it: its fields single-spaced. */
export function normalCron(cron: string): string {
  return cron.trim().split(/\s+/).join(' ')
}

function isZone(timezone: string): boolean {
  return Intl.supportedValuesOf('timeZone').includes(timezone) || timezone === 'UTC'
}

/** The server's checks of a cron and a zone that the mock can make: five fields, a sane length, a known IANA zone. */
export function validateCronAndZone(
  cron: string,
  timezone: string,
): Schemas['ValidationProblem'] | null {
  const normal = normalCron(cron)
  if (normal.split(' ').length !== CRON_FIELDS) {
    return rejected('validation_failed', 'cron', 'must have five fields')
  }
  if (normal.length > MAX_CRON_LENGTH) {
    return rejected('validation_failed', 'cron', 'cron is longer than 200 characters')
  }
  if (!isZone(timezone)) {
    return rejected('validation_failed', 'timezone', 'is not an IANA time zone')
  }
  return null
}

/** The server's checks of a schedule that the mock can make. */
export function validateSchedule(
  schedule: Schemas['ScheduleInput'],
): Schemas['ValidationProblem'] | null {
  return validateCronAndZone(schedule.cron, schedule.timezone)
}

/** The preview refuses what saving refuses, and a language that is neither ru nor en. */
export function validatePreview(
  cron: string,
  timezone: string,
  lang: string | null,
): Schemas['ValidationProblem'] | null {
  if (lang !== null && lang !== 'ru' && lang !== 'en') {
    return rejected('validation_failed', 'lang', 'must be ru or en')
  }
  return validateCronAndZone(cron, timezone)
}

/** The server's checks of a source that the mock can make: its agent, plugin and repository exist. */
export function validateSource(
  source: Schemas['SourceInput'],
  agents: Schemas['AgentDetails'][],
): Schemas['ValidationProblem'] | null {
  const agent = agents.find((a) => a.id === source.agentId)
  if (agent === undefined) return rejected('unknown_agent', 'agentId', 'no such agent')
  if (agent.revokedAt !== null) return rejected('agent_revoked', 'agentId', 'the agent is revoked')
  if (!agent.plugins.some((p) => p.name === source.plugin && p.actions.includes('backup'))) {
    return rejected(
      'unknown_plugin',
      'plugin',
      `the agent has no plugin ${source.plugin} that offers backup`,
    )
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
