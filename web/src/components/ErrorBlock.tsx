// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Group } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { describeError } from '../api/call'
import { tones } from '../theme'

// A failed request as a localized message by its machine code, with a retry when asked to.
export function ErrorBlock({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const { t } = useTranslation()
  const message = describeError(error)
  return (
    <Alert color={tones.error} role="alert">
      <Group justify="space-between">
        <span>{t(message.key, message.params)}</span>
        {onRetry && (
          <Button variant="light" size="xs" onClick={onRetry}>
            {t('common.retry')}
          </Button>
        )}
      </Group>
    </Alert>
  )
}
