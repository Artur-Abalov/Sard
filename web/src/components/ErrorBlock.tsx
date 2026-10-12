// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Group } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { ApiError, describeError } from '../api/call'
import { errorLink } from '../errors'
import { tones } from '../theme'
import { AppLink } from './links'

// A failed request as a localized message by its machine code, with a retry when asked to.
export function ErrorBlock({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t } = useTranslation()
  const message = describeError(error)
  const link = error instanceof ApiError ? errorLink(error.failure) : null
  return (
    <Alert color={tones.error} role="alert">
      <Group justify="space-between">
        <span>
          {t(message.key, message.params)}
          {link && (
            <>
              {' '}
              <AppLink to={link.to}>{t('errors.openWizard')}</AppLink>
            </>
          )}
        </span>
        {onRetry && (
          <Button variant="light" size="xs" onClick={onRetry}>
            {t('common.retry')}
          </Button>
        )}
      </Group>
    </Alert>
  )
}
