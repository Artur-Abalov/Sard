// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Stack } from '@mantine/core'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { ApiError, call } from '../api/call'
import { client } from '../api/client'
import { ErrorBlock } from './ErrorBlock'
import { AppLink } from './links'
import { tones } from '../theme'

// The run that is going already, from a 409 run_active; null for any other answer.
function activeRunOf(error: unknown): string | null {
  if (!(error instanceof ApiError) || error.failure.kind !== 'problem') return null
  if (error.failure.code !== 'run_active') return null
  const body = error.body as { activeRunId?: string }
  return body.activeRunId ?? null
}

// "Run backup": POST the run, then open its card. A source that is backing up already is
// not an error: the answer is a note, with a link to the run that is going.
export function RunBackupButton({ sourceId }: { sourceId: string }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const start = useMutation({
    mutationFn: () =>
      call(client.POST('/api/v1/sources/{sourceId}/runs', { params: { path: { sourceId } } })),
    onSuccess: async (run) => {
      void queryClient.invalidateQueries({ queryKey: ['runs'] })
      await navigate({ to: '/runs/$runId', params: { runId: run.id } })
    },
  })
  const active = activeRunOf(start.error)
  return (
    <Stack gap="xs" align="flex-start">
      <Button loading={start.isPending} onClick={() => start.mutate()}>
        {t('sources.runBackup')}
      </Button>
      {active !== null && (
        <Alert color={tones.info} role="status">
          {t('sources.alreadyRunning')}{' '}
          <AppLink to="/runs/$runId" params={{ runId: active }}>
            {t('sources.openRun')}
          </AppLink>
        </Alert>
      )}
      {start.isError && active === null && <ErrorBlock error={start.error} />}
    </Stack>
  )
}
