// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { components } from '../api/schema'
import { outcomeIcon, tones } from '../theme'
import { outcomeView } from '../verification'
import { StatusIcon } from './StatusIcon'

// How a verification ended: a failed backup is an error, a check that did not take place a warning, and the two
// never share one red badge (V-D10). The label is the text, the color and the icon only help.
export function OutcomeBadge({ status }: { status: components['schemas']['VerificationOutcome'] }) {
  const { i18n } = useTranslation()
  const { label, role } = outcomeView(status, i18n.language)
  return (
    <Badge color={tones[role]} leftSection={<StatusIcon name={outcomeIcon(status)} />}>
      {label}
    </Badge>
  )
}
