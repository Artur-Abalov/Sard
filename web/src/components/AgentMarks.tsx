// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge, Group } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { components } from '../api/schema'
import { useFormat } from '../useFormat'
import { tones } from '../theme'

type Agent = Pick<components['schemas']['AgentSummary'], 'revokedAt' | 'duplicateSessionAt'>

// The notes an agent carries beside its status: revoked, and a confirmed duplicate session (S5b).
export function AgentMarks({ agent }: { agent: Agent }) {
  const { t } = useTranslation()
  const format = useFormat()
  return (
    <Group gap="xs">
      {agent.revokedAt !== null && (
        <Badge color={tones.error}>
          {t('agents.revokedAt', { time: format.time(agent.revokedAt) })}
        </Badge>
      )}
      {agent.duplicateSessionAt !== null && (
        <Badge color={tones.notice}>
          {t('agents.duplicateAt', { time: format.time(agent.duplicateSessionAt) })}
        </Badge>
      )}
    </Group>
  )
}
