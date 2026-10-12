// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, List, Loader, Stack, Text } from '@mantine/core'
import type { UseQueryResult } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { isRefusedValues } from '../api/call'
import type { components } from '../api/schema'
import { ErrorBlock } from './ErrorBlock'
import { tones } from '../theme'
import { useFormat } from '../useFormat'

type Preview = components['schemas']['SchedulePreview']

// The warning of the server that a schedule fires more than once in 15 minutes: only a warning.
export function FrequencyWarning({ preview }: { preview: Preview | undefined }) {
  const { t } = useTranslation()
  if (preview?.tooFrequent !== true) return null
  return (
    <Alert color={tones.warning} role="status">
      {t('schedule.tooFrequent')}
    </Alert>
  )
}

// Under the editor: the schedule in words and the next three fires in its zone, as the server says them.
export function SchedulePreview({
  query,
}: {
  query: Pick<UseQueryResult<Preview>, 'data' | 'error' | 'isError' | 'isFetching' | 'refetch'>
}) {
  const { t } = useTranslation()
  const format = useFormat()
  const { data } = query
  if (query.isError && !isRefusedValues(query.error)) {
    return <ErrorBlock error={query.error} onRetry={() => void query.refetch()} />
  }
  if (data === undefined) {
    return query.isFetching ? <Loader size="xs" aria-label={t('common.loading')} /> : null
  }
  return (
    <Stack gap="xs">
      <Text fw={600}>{data.description}</Text>
      <FrequencyWarning preview={data} />
      <Text size="sm" c="dimmed">
        {t('schedule.nextThree')}
      </Text>
      <List size="sm" listStyleType="none">
        {data.nextFires.map((fire) => (
          <List.Item key={fire}>{format.scheduleTime(fire, data.timezone)}</List.Item>
        ))}
      </List>
    </Stack>
  )
}
