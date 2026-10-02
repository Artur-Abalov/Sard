// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Stack, Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { CopyBox } from './CopyBox'

// How to initialize a repository the agent reports without an id: the host's command and the restart.
export function RepoInitHint({ repository }: { repository: string }) {
  const { t } = useTranslation()
  return (
    <Stack gap="xs">
      <Text size="sm">{t('agents.repoInitHint')}</Text>
      <CopyBox value={`sard-agent repo init ${repository}`} label={t('agents.repoInitCommand')} />
      <Text size="sm">{t('agents.repoInitRestart')}</Text>
    </Stack>
  )
}
