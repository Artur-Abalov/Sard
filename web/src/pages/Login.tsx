// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Card, PasswordInput, Stack, Title } from '@mantine/core'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { client } from '../api/client'
import { formatLockoutMinutes } from '../auth/lockout'
import { resolveRedirectTarget } from '../auth/redirect'

type FormError = { kind: 'invalid' } | { kind: 'locked'; minutes: number } | { kind: 'unavailable' }

// The single-field sign-in form (rule "Форма входа — одно поле пароля"). Password
// checking, the session and its lifetime are the server's (D2): this component only
// collects the password, shows the server's answer and follows the redirect.
export function Login() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const search = useSearch({ strict: false }) as { redirect?: string }
  const [password, setPassword] = useState('')
  const [error, setError] = useState<FormError | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function onSubmit(event: React.FormEvent) {
    event.preventDefault()
    if (password === '') return
    setSubmitting(true)
    setError(null)
    try {
      const { response } = await client.POST('/api/v1/session', { body: { password } })
      if (response.status === 204) {
        await navigate({ to: resolveRedirectTarget(search.redirect) })
        return
      }
      if (response.status === 429) {
        const retryAfter = Number(response.headers.get('Retry-After') ?? '0')
        setError({ kind: 'locked', minutes: formatLockoutMinutes(retryAfter) })
      } else if (response.status === 401) {
        setError({ kind: 'invalid' })
      } else {
        setError({ kind: 'unavailable' })
      }
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
            {error?.kind === 'invalid' && <Alert color="red">{t('login.wrongPassword')}</Alert>}
            {error?.kind === 'locked' && (
              <Alert color="red">{t('login.locked', { minutes: error.minutes })}</Alert>
            )}
            {error?.kind === 'unavailable' && <Alert color="red">{t('login.unavailable')}</Alert>}
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
