// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge, Group } from '@mantine/core'
import { Trans } from 'react-i18next'
import type { components } from '../api/schema'
import { tones } from '../theme'
import { StatusIcon } from './StatusIcon'
import { Time } from './Time'

type Agent = Pick<components['schemas']['AgentSummary'], 'revokedAt' | 'duplicateSessionAt'>

// The notes an agent carries beside its status: revoked, and a confirmed duplicate session (S5b).
export function AgentMarks({ agent }: { agent: Agent }) {
  return (
    <Group gap="xs">
      {agent.revokedAt !== null && (
        <Badge color={tones.error} leftSection={<StatusIcon name="x-circle" />}>
          <Trans
            i18nKey="agents.revokedAt"
            components={{ when: <Time value={agent.revokedAt} /> }}
          />
        </Badge>
      )}
      {agent.duplicateSessionAt !== null && (
        <Badge color={tones.notice} leftSection={<StatusIcon name="clock" />}>
          <Trans
            i18nKey="agents.duplicateAt"
            components={{ when: <Time value={agent.duplicateSessionAt} /> }}
          />
        </Badge>
      )}
    </Group>
  )
}
