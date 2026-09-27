// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Card, Loader, Stack, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { fetchStatus } from '../api/client'
import { formatTimestamp } from '../format'

export function Dashboard() {
  const { t, i18n } = useTranslation()
  const status = useQuery({ queryKey: ['status'], queryFn: fetchStatus })

  return (
    <Stack>
      <Title order={2}>{t('dashboard.title')}</Title>
      <Card withBorder padding="lg">
        {status.isPending && <Loader size="sm" aria-label={t('dashboard.loading')} />}
        {status.isError && <Alert color="red">{t('dashboard.error')}</Alert>}
        {status.data && (
          <Stack gap="xs">
            <Text size="xl" fw={600}>
              {t('dashboard.lastVerifiedRestore', {
                value: formatTimestamp(status.data.lastVerifiedRestoreAt, i18n.language),
              })}
            </Text>
            <Text c="dimmed" size="sm">
              {t('dashboard.serverVersion', { version: status.data.version })}
            </Text>
          </Stack>
        )}
      </Card>
    </Stack>
  )
}
