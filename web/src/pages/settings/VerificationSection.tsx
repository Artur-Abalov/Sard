// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, NumberInput, Stack, Text, TextInput, Title } from '@mantine/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, call } from '../../api/call'
import { client } from '../../api/client'
import { selfBackupQuery, sourceQuery, verificationScheduleQuery } from '../../api/queries'
import type { components } from '../../api/schema'
import { ErrorBlock } from '../../components/ErrorBlock'
import { AppLink } from '../../components/links'
import { Loaded } from '../../components/Loaded'
import { Mono } from '../../components/Mono'
import { VerificationMetric } from '../../components/VerificationMetric'
import { fieldFailures } from '../../errors'
import { tones } from '../../theme'
import { useFormat } from '../../useFormat'

type Schedule = components['schemas']['VerificationSchedule']

// The text the server's refusal puts next to [field] of the form; undefined when it did not name the field.
function refusal(error: unknown, field: string, text: (key: string) => string): string | undefined {
  const failures = error instanceof ApiError ? fieldFailures(error.body) : []
  const found = failures.find((failure) => failure.field === field)
  return found === undefined || !('key' in found.text) ? undefined : text(found.text.key)
}

function CycleForm({ schedule }: { schedule: Schedule }) {
  const { t } = useTranslation()
  const format = useFormat()
  const queryClient = useQueryClient()
  const [cron, setCron] = useState(schedule.cron)
  const [timezone, setTimezone] = useState(schedule.timezone)
  const [days, setDays] = useState<number | string>(schedule.staleAfterDays)
  const [saved, setSaved] = useState(false)
  const save = useMutation({
    mutationFn: () =>
      call(
        client.PUT('/api/v1/verification-schedule', {
          body: { cron, timezone, staleAfterDays: Number(days) },
        }),
      ),
    onSuccess: (next) => {
      setSaved(true)
      queryClient.setQueryData(verificationScheduleQuery().queryKey, next)
    },
    onMutate: () => setSaved(false),
  })
  const field = (name: string) => refusal(save.error, name, (key) => t(key))
  return (
    <Stack maw={420}>
      <Text>
        {t('settings.verification.next')}: <Mono>{format.time(schedule.nextRunAt)}</Mono>
      </Text>
      <TextInput
        label={t('settings.verification.cron')}
        value={cron}
        onChange={(event) => setCron(event.currentTarget.value)}
        error={field('cron')}
      />
      <TextInput
        label={t('settings.verification.timezone')}
        value={timezone}
        onChange={(event) => setTimezone(event.currentTarget.value)}
        error={field('timezone')}
      />
      <NumberInput
        label={t('settings.verification.staleAfterDays')}
        description={t('settings.verification.staleAfterHint')}
        value={days}
        onChange={setDays}
        min={1}
        max={90}
        allowDecimal={false}
        error={field('staleAfterDays')}
      />
      {save.isError &&
        ['cron', 'timezone', 'staleAfterDays'].every((name) => field(name) === undefined) && (
          <ErrorBlock error={save.error} />
        )}
      {saved && (
        <Alert color={tones.success} role="status">
          {t('settings.verification.saved')}
        </Alert>
      )}
      <Button loading={save.isPending} onClick={() => save.mutate()}>
        {t('settings.verification.save')}
      </Button>
    </Stack>
  )
}

// A system source of the self-backup with its metric: it is verified like any other (rule 3).
function SelfBackupMetric({ sourceId }: { sourceId: string }) {
  const source = useQuery(sourceQuery(sourceId))
  if (source.data === undefined) return null
  return (
    <Text>
      <AppLink to="/sources/$sourceId" params={{ sourceId }}>
        <Mono>{source.data.name}</Mono>
      </AppLink>
      {': '}
      <VerificationMetric
        enabled={source.data.verificationEnabled}
        lastVerifiedAt={source.data.lastVerifiedAt}
        lastVerifiedLevel={source.data.lastVerifiedLevel}
      />
    </Text>
  )
}

function SelfBackupMetrics() {
  const { t } = useTranslation()
  const selfBackup = useQuery(selfBackupQuery())
  const sources = selfBackup.data?.sources ?? []
  if (sources.length === 0) return null
  return (
    <Stack gap="xs">
      <Title order={5}>{t('settings.verification.selfBackup')}</Title>
      {sources.map((source) => (
        <SelfBackupMetric key={source.sourceId} sourceId={source.sourceId} />
      ))}
    </Stack>
  )
}

// The section «Restore verification» of the settings (rule 14): the time of the daily cycle, its zone and the
// threshold of «not verified for longer than N days», and the metric of the self-backup, which is verified too.
export function VerificationSection() {
  const { t } = useTranslation()
  const schedule = useQuery(verificationScheduleQuery())
  return (
    <Stack maw={640}>
      <Title order={3}>{t('settings.verification.title')}</Title>
      <Loaded query={schedule}>{(data) => <CycleForm schedule={data} />}</Loaded>
      <SelfBackupMetrics />
    </Stack>
  )
}
