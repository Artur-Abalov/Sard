// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { incompleteMarkShown } from '../stepNotes'
import { tones } from '../theme'

// The text mark beside a status when the server says the backup did not finish but made a snapshot.
export function IncompleteMark({ run }: { run: { partial: boolean } }) {
  const { t } = useTranslation()
  if (!incompleteMarkShown(run)) return null
  return (
    <Badge color={tones.warning} variant="outline" title={t('incomplete.markWhy')}>
      {t('incomplete.mark')}
    </Badge>
  )
}
