// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Stack, Text } from '@mantine/core'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

// "Not found" for a card whose object does not exist, with a link back to the list.
export function NotFoundCard({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  return (
    <Stack align="flex-start">
      <Text role="alert">{t('errors.not_found')}</Text>
      {children}
    </Stack>
  )
}
