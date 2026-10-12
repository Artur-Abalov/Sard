// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Button, PasswordInput, Stack, Text, Title } from '@mantine/core'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { NoticeAlert } from '../../components/NoticeAlert'
import { canSetPassword, confirmationMismatch } from '../../onboarding/forms'
import { setAdminPassword } from '../../onboarding/api'
import { adminStepResult, type Notice, type StepResult } from '../../onboarding/outcomes'

// The administrator password and its repetition. The length is the server's to judge: the
// requirements are text, a refusal comes back as a 422.
export function AdminScreen({ onResult }: { onResult: (result: StepResult) => void }) {
  const { t } = useTranslation()
  const [password, setPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [notice, setNotice] = useState<Notice | null>(null)

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (!canSetPassword(password, confirmation)) return
    setSubmitting(true)
    const result = adminStepResult(await setAdminPassword(password))
    setSubmitting(false)
    setNotice(result.kind === 'stay' ? result.notice : null)
    if (result.kind !== 'stay') {
      setPassword('')
      setConfirmation('')
    }
    onResult(result)
  }

  return (
    <form onSubmit={(event) => void submit(event)}>
      <Stack>
        <Title order={3}>{t('setup.admin.title')}</Title>
        {notice && <NoticeAlert notice={notice} />}
        <Text>{t('setup.admin.requirements')}</Text>
        <PasswordInput
          label={t('setup.admin.password')}
          value={password}
          onChange={(event) => setPassword(event.currentTarget.value)}
          autoComplete="new-password"
          data-autofocus
        />
        <PasswordInput
          label={t('setup.admin.confirmation')}
          value={confirmation}
          onChange={(event) => setConfirmation(event.currentTarget.value)}
          autoComplete="new-password"
          error={confirmationMismatch(password, confirmation) ? t('setup.admin.mismatch') : null}
        />
        <Button
          type="submit"
          disabled={!canSetPassword(password, confirmation) || submitting}
          loading={submitting}
        >
          {t('setup.admin.submit')}
        </Button>
      </Stack>
    </form>
  )
}
