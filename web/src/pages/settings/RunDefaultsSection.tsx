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
import { timeoutOfField } from '../../sourceDraft'
import { tones } from '../../theme'

type RunDefaults = components['schemas']['RunDefaults']

const MAX_MINUTES = 1440

function RunDefaultsForm({ saved }: { saved: RunDefaults }) {
  const { t } = useTranslation()
  const text = useErrorText()
  const queryClient = useQueryClient()
  const [backup, setBackup] = useState<number | string>(saved.backupTimeoutMinutes)
  const [restore, setRestore] = useState<number | string>(saved.restoreTimeoutMinutes)
  const save = useMutation({
    mutationFn: (input: RunDefaults) => call(client.PUT('/api/v1/run-defaults', { body: input })),
    onSuccess: (answer) => queryClient.setQueryData(runDefaultsQuery().queryKey, answer),
  })
  const failures = save.error instanceof ApiError ? fieldFailures(save.error.body) : []
  const errors = groupFieldErrors(failures, [])
  const input = {
    backupTimeoutMinutes: timeoutOfField(backup),
    restoreTimeoutMinutes: timeoutOfField(restore),
  }
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
        max={MAX_MINUTES}
        allowDecimal={false}
        value={backup}
        onChange={setBackup}
        error={text(errors.backupTimeout)}
      />
      <NumberInput
        label={t('settings.runDefaults.restore')}
        min={1}
        max={MAX_MINUTES}
        allowDecimal={false}
        value={restore}
        onChange={setRestore}
        error={text(errors.restoreTimeout)}
      />
      {save.isError && failures.length === 0 && <ErrorBlock error={save.error} />}
      <Button
        loading={save.isPending}
        disabled={input.backupTimeoutMinutes === null || input.restoreTimeoutMinutes === null}
        onClick={() =>
          save.mutate({
            backupTimeoutMinutes: input.backupTimeoutMinutes ?? saved.backupTimeoutMinutes,
            restoreTimeoutMinutes: input.restoreTimeoutMinutes ?? saved.restoreTimeoutMinutes,
          })
        }
      >
        {t('settings.runDefaults.submit')}
      </Button>
    </Stack>
  )
}

// The installation's default step timeouts (J1b): the server holds them and checks them (1 to 1440 minutes).
export function RunDefaultsSection() {
  const defaults = useQuery(runDefaultsQuery())
  return <Loaded query={defaults}>{(data) => <RunDefaultsForm saved={data} />}</Loaded>
}
