// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Card, PasswordInput, Stack, Title } from '@mantine/core'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { resolveRedirectTarget } from '../auth/redirect'
import { signIn } from '../auth/session'
import { signInResult, type SignInResult } from '../auth/signInResult'
import { tones } from '../theme'

function SignInError({ error }: { error: SignInResult }) {
  const { t } = useTranslation()
  switch (error.kind) {
    case 'invalid':
      return <Alert color={tones.error}>{t('login.wrongPassword')}</Alert>
    case 'locked':
      return <Alert color={tones.error}>{t('login.locked', { minutes: error.minutes })}</Alert>
    case 'unavailable':
      return <Alert color={tones.error}>{t('login.unavailable')}</Alert>
    default:
      return null
  }
}

// The single-field sign-in form (rule "Форма входа — одно поле пароля"). Password
// checking, the session and its lifetime are the server's (D2): this component only
// collects the password, shows the server's answer and follows the redirect.
export function Login() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const search = useSearch({ strict: false }) as { redirect?: string; notice?: 'setup_completed' }
  const [password, setPassword] = useState('')
  const [error, setError] = useState<SignInResult | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function onSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (password === '') return
    setSubmitting(true)
    setError(null)
    try {
      const response = await signIn(password)
      const result = signInResult(response.status, response.headers.get('Retry-After'))
      if (result.kind === 'ok') {
        await navigate({ to: resolveRedirectTarget(search.redirect) })
        return
      }
      if (result.kind === 'setup') {
        await navigate({ to: '/setup' })
        return
      }
      setError(result)
    } catch {
      setError({ kind: 'unavailable' })
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Stack align="center" justify="center" mih="100vh">
      <Card withBorder padding="lg" miw={320}>
        <form onSubmit={(event) => void onSubmit(event)}>
          <Stack>
            <Title order={2}>{t('login.title')}</Title>
            {search.notice === 'setup_completed' && (
              <Alert color={tones.info}>{t('setup.completed')}</Alert>
            )}
            {error && <SignInError error={error} />}
            <PasswordInput
              label={t('login.password')}
              value={password}
              onChange={(event) => setPassword(event.currentTarget.value)}
              autoComplete="current-password"
              data-autofocus
            />
            <Button type="submit" disabled={password === '' || submitting} loading={submitting}>
              {t('login.submit')}
            </Button>
          </Stack>
        </form>
      </Card>
    </Stack>
  )
}
