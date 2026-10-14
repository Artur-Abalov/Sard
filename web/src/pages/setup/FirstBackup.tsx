// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Group, Stack, Text, Title } from '@mantine/core'
import { useQueries, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { runQuery } from '../../api/queries'
import type { components } from '../../api/schema'
import { AppLink } from '../../components/links'
import { StatusBadge } from '../../components/StatusBadge'
import { startFirstBackup } from '../../onboarding/selfBackup'
import { tones } from '../../theme'

type Started = components['schemas']['SelfBackupRunStarted']
type Run = components['schemas']['Run']

const LABELS = {
  self_database: 'setup.selfBackup.first.database',
  self_keys: 'setup.selfBackup.first.keys',
} as const

// The message of the failed step as the server wrote it, or the run's own.
function failureMessage(run: Run): string | null {
  return run.steps.find((step) => step.message !== null)?.message ?? run.message
}

function Failure({ run }: { run: Run }) {
  const { t } = useTranslation()
  const message = failureMessage(run)
  return (
    <Text size="sm" c={tones.error} role="alert">
      {message === null
        ? t('setup.selfBackup.first.failedNoMessage')
        : t('setup.selfBackup.first.failed', { message })}
    </Text>
  )
}

// A run of one system source: its status as the server reports it and, when it failed, why.
function RunLine({ started, run }: { started: Started; run: Run | undefined }) {
  const { t } = useTranslation()
  return (
    <Stack gap={2}>
      <Group gap="sm">
        <Text fw={500}>{t(LABELS[started.role])}</Text>
        {run && <StatusBadge group="RunStatus" value={run.status} />}
        <AppLink to="/runs/$runId" params={{ runId: started.runId }} size="sm">
          {t('setup.selfBackup.first.openRun')}
        </AppLink>
      </Group>
      {run?.status === 'failed' && <Failure run={run} />}
    </Stack>
  )
}

// The first backup, which cannot be skipped (Рк13): "back up now", the progress of both runs by the rhythm of W2, and
// the same request again when one failed. The step is done when the server says so: the wizard follows its state.
export function FirstBackup() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [started, setStarted] = useState<Started[]>([])
  const [starting, setStarting] = useState(false)
  const [refused, setRefused] = useState(false)
  const runs = useQueries({ queries: started.map((run) => runQuery(run.runId)) })
  const statuses = runs.map((run) => run.data?.status)
  const failed = statuses.includes('failed')
  const succeeded = started.length > 0 && statuses.every((status) => status === 'succeeded')

  useEffect(() => {
    if (!succeeded) return
    void queryClient.invalidateQueries({ queryKey: ['self-backup'] })
    void queryClient.invalidateQueries({ queryKey: ['onboarding'] })
  }, [succeeded, queryClient])

  async function start() {
    setStarting(true)
    setRefused(false)
    const result = await startFirstBackup()
    setStarting(false)
    if (result.ok) setStarted(result.runs)
    else setRefused(true)
  }

  return (
    <Stack>
      <Title order={4}>{t('setup.selfBackup.first.title')}</Title>
      <Text size="sm">{t('setup.selfBackup.first.intro')}</Text>
      {started.map((run, index) => (
        <RunLine key={run.runId} started={run} run={runs[index]?.data} />
      ))}
      {succeeded && (
        <Text size="sm" role="status">
          {t('setup.selfBackup.first.done')}
        </Text>
      )}
      {refused && (
        <Alert color={tones.error} role="alert">
          {t('setup.selfBackup.first.unavailable')}
        </Alert>
      )}
      {(started.length === 0 || failed) && (
        <Group>
          <Button onClick={() => void start()} loading={starting}>
            {t(failed ? 'setup.selfBackup.first.retry' : 'setup.selfBackup.first.start')}
          </Button>
        </Group>
      )}
    </Stack>
  )
}
