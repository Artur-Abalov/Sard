// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { statusVisual } from '../theme'
import { StatusIcon } from './StatusIcon'

// A status as text with an icon and a color; the meaning never rests on the color alone.
export function StatusBadge({ group, value }: { group: string; value: string }) {
  const { t } = useTranslation()
  const { color, icon } = statusVisual(value)
  return (
    <Badge color={color} leftSection={<StatusIcon name={icon} />}>
      {t(`enum.${group}.${value}`)}
    </Badge>
  )
}
