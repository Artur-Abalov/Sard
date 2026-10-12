// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { Notice } from '../onboarding/outcomes'
import { tones } from '../theme'

// A message of a form: a key of src/locales, the lock time in minutes where it has one.
export function NoticeAlert({ notice, tone = tones.error }: { notice: Notice; tone?: string }) {
  const { t } = useTranslation()
  return (
    <Alert color={tone} role="alert">
      {t(notice.key, { minutes: notice.minutes })}
    </Alert>
  )
}
