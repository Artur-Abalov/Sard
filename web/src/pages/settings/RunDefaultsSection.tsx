// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, NumberInput, Stack, Text, Title } from '@mantine/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, call } from '../../api/call'
import { client } from '../../api/client'
import { runDefaultsQuery } from '../../api/queries'
import type { components } from '../../api/schema'
import { ErrorBlock } from '../../components/ErrorBlock'
import { Loaded } from '../../components/Loaded'
import { useErrorText } from '../../components/useErrorText'
import { fieldFailures } from '../../errors'
import { groupFieldErrors } from '../../fieldErrors'
import { hoursFieldOf, minutesOfHoursField, oddTimeout } from '../../sourceDraft'
import { useFormat } from '../../useFormat'
import { tones } from '../../theme'

type RunDefaults = components['schemas']['RunDefaults']

const MAX_HOURS = 24

function RunDefaultsForm({ saved }: { saved: RunDefaults }) {
  const { t } = useTranslation()
  const text = useErrorText()
  const queryClient = useQueryClient()
  const format = useFormat()
  // Minutes, as the API holds them: a stored value that is not whole hours stays as it is until a new one is typed.
  const [backup, setBackup] = useState<number | null>(saved.backupTimeoutMinutes)
  const [restore, setRestore] = useState<number | null>(saved.restoreTimeoutMinutes)
  const stored = (minutes: number | null) => {
    const odd = oddTimeout(minutes)
    return odd === null ? undefined : t('form.timeoutStored', { value: format.timeout(odd * 60) })
  }
  const save = useMutation({
    mutationFn: (input: RunDefaults) => call(client.PUT('/api/v1/run-defaults', { body: input })),
    onSuccess: (answer) => queryClient.setQueryData(runDefaultsQuery().queryKey, answer),
  })
  const failures = save.error instanceof ApiError ? fieldFailures(save.error.body) : []
  const errors = groupFieldErrors(failures, [])
  return (
    <Stack maw={420}>
      <Title order={3}>{t('settings.runDefaults.title')}</Title>
      <Text>{t('settings.runDefaults.hint')}</Text>
      {save.isSuccess && (
        <Alert color={tones.success} role="status">
          {t('settings.runDefaults.saved')}
        </Alert>
      )}
      <NumberInput
        label={t('settings.runDefaults.backup')}
        min={1}
        max={MAX_HOURS}
        allowDecimal={false}
        description={stored(backup)}
        value={hoursFieldOf(backup)}
        onChange={(value) => setBackup(minutesOfHoursField(value))}
        error={text(errors.backupTimeout)}
      />
      <NumberInput
        label={t('settings.runDefaults.restore')}
        min={1}
        max={MAX_HOURS}
        allowDecimal={false}
        description={stored(restore)}
        value={hoursFieldOf(restore)}
        onChange={(value) => setRestore(minutesOfHoursField(value))}
        error={text(errors.restoreTimeout)}
      />
      {save.isError && failures.length === 0 && <ErrorBlock error={save.error} />}
      <Button
        loading={save.isPending}
        disabled={backup === null || restore === null}
        onClick={() =>
          save.mutate({
            backupTimeoutMinutes: backup ?? saved.backupTimeoutMinutes,
            restoreTimeoutMinutes: restore ?? saved.restoreTimeoutMinutes,
          })
        }
      >
        {t('settings.runDefaults.submit')}
      </Button>
    </Stack>
  )
}

// The installation's default step timeouts (J1b): the server holds them and checks them (1 to 24 hours in the console, minutes in the API).
export function RunDefaultsSection() {
  const defaults = useQuery(runDefaultsQuery())
  return <Loaded query={defaults}>{(data) => <RunDefaultsForm saved={data} />}</Loaded>
}
