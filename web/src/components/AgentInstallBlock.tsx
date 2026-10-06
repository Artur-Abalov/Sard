// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Button, Card, Group, Loader, Stack, Text, Title } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { installQuery } from '../api/queries'
import { INSTALL_DEFAULTS, withEnrollCommand } from '../install'
import { DownloadsOff } from './DownloadsOff'
import { ErrorBlock } from './ErrorBlock'
import { InstallChoices } from './InstallChoices'
import { InstallSteps } from './InstallSteps'

function InstallBody({ enrollCommand }: { enrollCommand: string | undefined }) {
  const { t } = useTranslation()
  const [choice, setChoice] = useState(INSTALL_DEFAULTS)
  const install = useQuery({ ...installQuery(choice), placeholderData: keepPreviousData })
  if (install.data === undefined) {
    if (install.isError)
      return <ErrorBlock error={install.error} onRetry={() => void install.refetch()} />
    return <Loader size="sm" aria-label={t('common.loading')} />
  }
  const data = install.data
  if (!data.downloadsEnabled) return <DownloadsOff manualInstallDoc={data.manualInstallDoc} />
  const steps =
    enrollCommand === undefined ? data.steps : withEnrollCommand(data.steps, enrollCommand)
  return (
    <Stack>
      {install.isError && (
        <ErrorBlock error={install.error} onRetry={() => void install.refetch()} />
      )}
      <InstallChoices choice={choice} onChange={setChoice} withArch />
      <Text size="sm">
        {t('install.versions', { agent: data.agentVersion, restic: data.resticVersion })}
      </Text>
      {steps.length === 0 ? (
        <Text c="dimmed">{t('install.noSteps')}</Text>
      ) : (
        <InstallSteps steps={steps} signed={data.signed} releaseKey={data.releaseKey} />
      )}
    </Stack>
  )
}

// How to install an agent on a new host: the choice of package and the server's steps. Open where
// there is nothing else to do (no agents yet, the dialog of a new token), otherwise folded away.
// In the dialog [enrollCommand] is that of the new token and stands in the enroll step.
export function AgentInstallBlock({
  open,
  enrollCommand,
}: {
  open?: boolean
  enrollCommand?: string
}) {
  const { t } = useTranslation()
  const [opened, { toggle }] = useDisclosure(open === true)
  return (
    <Card withBorder>
      <Stack>
        <Group justify="space-between">
          <Title order={4}>{t('install.title')}</Title>
          {open !== true && (
            <Button variant="default" size="xs" onClick={toggle} aria-expanded={opened}>
              {opened ? t('install.hide') : t('install.show')}
            </Button>
          )}
        </Group>
        {opened && <InstallBody enrollCommand={enrollCommand} />}
      </Stack>
    </Card>
  )
}
