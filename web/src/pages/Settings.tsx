// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, PasswordInput, Stack, Text, Title } from '@mantine/core'
import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { NoticeAlert } from '../components/NoticeAlert'
import { changePassword } from '../onboarding/api'
import { canChangePassword, confirmationMismatch } from '../onboarding/forms'
import { passwordChangeResult, type Notice } from '../onboarding/outcomes'
import { tones } from '../theme'

type Outcome = { kind: 'changed' } | { kind: 'failed'; notice: Notice } | null

// The administrator's settings: for now the password only. The server checks the current
// password and the new one; this form only collects them and shows the answer (Рк6, Рк7).
export function Settings() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [current, setCurrent] = useState('')
  const [password, setPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [outcome, setOutcome] = useState<Outcome>(null)

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (!canChangePassword(current, password, confirmation)) return
    setSubmitting(true)
    const result = passwordChangeResult(await changePassword(current, password))
    setSubmitting(false)
    if (result.kind === 'goto') {
      await navigate({ to: '/login', search: { redirect: '/settings' } })
      return
    }
    setOutcome(
      result.kind === 'done' ? { kind: 'changed' } : { kind: 'failed', notice: result.notice },
    )
    if (result.kind === 'done') {
      setCurrent('')
      setPassword('')
      setConfirmation('')
    }
  }

  return (
    <Stack>
      <Title order={2}>{t('settings.title')}</Title>
      <form onSubmit={(event) => void submit(event)}>
        <Stack maw={420}>
          <Title order={3}>{t('settings.password.title')}</Title>
          {outcome?.kind === 'changed' && (
            <Alert color={tones.success} role="status">
              {t('settings.password.changed')}
            </Alert>
          )}
          {outcome?.kind === 'failed' && <NoticeAlert notice={outcome.notice} />}
          <Text>{t('settings.password.requirements')}</Text>
          <PasswordInput
            label={t('settings.password.current')}
            value={current}
            onChange={(event) => setCurrent(event.currentTarget.value)}
            autoComplete="current-password"
          />
          <PasswordInput
            label={t('settings.password.new')}
            value={password}
            onChange={(event) => setPassword(event.currentTarget.value)}
            autoComplete="new-password"
          />
          <PasswordInput
            label={t('settings.password.confirmation')}
            value={confirmation}
            onChange={(event) => setConfirmation(event.currentTarget.value)}
            autoComplete="new-password"
            error={
              confirmationMismatch(password, confirmation) ? t('settings.password.mismatch') : null
            }
          />
          <Button
            type="submit"
            disabled={!canChangePassword(current, password, confirmation) || submitting}
            loading={submitting}
          >
            {t('settings.password.submit')}
          </Button>
        </Stack>
      </form>
    </Stack>
  )
}
