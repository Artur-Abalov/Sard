// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Anchor, Card, Loader, Stack, Text, Title } from '@mantine/core'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { upgradeQuery, type AgentDetails } from '../api/queries'
import type { components } from '../api/schema'
import { INSTALL_DEFAULTS } from '../install'
import { tones } from '../theme'
import { DownloadsOff } from './DownloadsOff'
import { ErrorBlock } from './ErrorBlock'
import { InstallChoices } from './InstallChoices'
import { InstallSteps } from './InstallSteps'

type Upgrade = components['schemas']['AgentUpgrade']

// Why there are no commands for this agent, and where the manual way is described.
function NoCommands({ upgrade, reason }: { upgrade: Upgrade; reason: string }) {
  const { t } = useTranslation()
  return (
    <Stack gap="xs" align="flex-start">
      <Text>{reason}</Text>
      <Anchor href={upgrade.manualInstallDoc} target="_blank" rel="noopener noreferrer">
        {t('install.manual')}
      </Anchor>
    </Stack>
  )
}

function UpgradeBody({ agent }: { agent: AgentDetails }) {
  const { t } = useTranslation()
  const [choice, setChoice] = useState(INSTALL_DEFAULTS)
  const query = useQuery({
    ...upgradeQuery(agent.id, choice.format, choice.fetch),
    placeholderData: keepPreviousData,
  })
  const upgrade = query.data
  if (upgrade === undefined) {
    if (query.isError)
      return <ErrorBlock error={query.error} onRetry={() => void query.refetch()} />
    return <Loader size="sm" aria-label={t('common.loading')} />
  }
  if (!upgrade.downloadsEnabled) return <DownloadsOff manualInstallDoc={upgrade.manualInstallDoc} />
  if (upgrade.reason !== null) {
    return <NoCommands upgrade={upgrade} reason={t(`install.reason.${upgrade.reason}`)} />
  }
  return (
    <Stack>
      <Text size="sm">{t('install.upgrade.available', { agent: upgrade.agentVersion })}</Text>
      <Alert color={tones.notice}>{t('install.upgrade.remind')}</Alert>
      <InstallChoices choice={choice} onChange={setChoice} withArch={false} />
      {choice.format === 'deb' && <Text size="sm">{t('install.upgrade.debNote')}</Text>}
      <InstallSteps steps={upgrade.steps} signed={upgrade.signed} releaseKey={upgrade.releaseKey} />
    </Stack>
  )
}

// The commands to bring an outdated agent to the version this server hands out: the server decides
// the package for the agent's architecture, or says why there is none.
export function AgentUpgradeBlock({ agent }: { agent: AgentDetails }) {
  const { t } = useTranslation()
  return (
    <Card withBorder>
      <Stack>
        <Title order={4}>{t('install.upgrade.title')}</Title>
        <UpgradeBody agent={agent} />
      </Stack>
    </Card>
  )
}
