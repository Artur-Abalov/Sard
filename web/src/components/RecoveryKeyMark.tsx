// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { components } from '../api/schema'
import { recoveryKeyTone } from '../theme'

// The flag of a repository's recovery key as the server reports it (V3, КВ4).
export function RecoveryKeyMark({ flag }: { flag: components['schemas']['RecoveryKeyFlag'] }) {
  const { t } = useTranslation()
  return <Badge color={recoveryKeyTone(flag)}>{t(`repositories.recoveryKey.${flag}`)}</Badge>
}
