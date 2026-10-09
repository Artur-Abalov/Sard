// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge, Group } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { Trans, useTranslation } from 'react-i18next'
import { installQuery } from '../api/queries'
import type { components } from '../api/schema'
import { INSTALL_DEFAULTS } from '../install'
import { tones } from '../theme'
import { StatusIcon } from './StatusIcon'
import { Time } from './Time'

type Agent = Pick<
  components['schemas']['AgentSummary'],
  'revokedAt' | 'duplicateSessionAt' | 'outdated' | 'builtin'
>

// An update is available: a notice, not an error - the agent works. The version offered is the one
// of the install block's answer (the server's), shown once it is known.
function OutdatedMark() {
  const { t } = useTranslation()
  const install = useQuery(installQuery(INSTALL_DEFAULTS))
  return (
    <Badge color={tones.notice} leftSection={<StatusIcon name="arrow" />}>
      {install.data === undefined
        ? t('agents.outdatedPlain')
        : t('agents.outdated', { version: install.data.agentVersion })}
    </Badge>
  )
}

// The notes an agent carries beside its status: built in (sard-self), revoked, a confirmed duplicate session (S5b), outdated.
export function AgentMarks({ agent }: { agent: Agent }) {
  const { t } = useTranslation()
  return (
    <Group gap="xs">
      {agent.builtin && <Badge color={tones.info}>{t('agents.builtin')}</Badge>}
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
      {agent.outdated && <OutdatedMark />}
    </Group>
  )
}
