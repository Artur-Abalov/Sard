// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Stack, Text, Title } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { CopyBox } from '../../components/CopyBox'

// No usable code (expired, or never issued): only a restart of the server brings a new one (Рк4).
export function RestartScreen() {
  const { t } = useTranslation()
  return (
    <Stack>
      <Title order={3}>{t('setup.restart.title')}</Title>
      <Text>{t('setup.restart.message')}</Text>
      <CopyBox value={t('setup.restart.command')} label="" />
    </Stack>
  )
}
