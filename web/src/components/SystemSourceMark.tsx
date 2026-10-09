// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { components } from '../api/schema'
import { tones } from '../theme'

type Source = Pick<components['schemas']['Source'], 'systemRole'>

// A source the server keeps itself (F6, the self-backup): marked wherever the source is named.
export function SystemSourceMark({ source }: { source: Source }) {
  const { t } = useTranslation()
  if (source.systemRole === null) return null
  return (
    <Badge color={tones.info} title={t('source.systemWhy')}>
      {t('source.system')}
    </Badge>
  )
}
