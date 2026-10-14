// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, List } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { selfBackupQuery } from '../api/queries'
import { bannerLink, bannerTone } from '../selfBackupBanner'
import { tones } from '../theme'
import { useFormat } from '../useFormat'
import { AppLink } from './links'

// The self-backup's warnings on every page (F6): shown while there are any, never blocking.
// Without an answer (still loading, the server unreachable) there is nothing to say here.
export function SelfBackupBanner() {
  const { t } = useTranslation()
  const format = useFormat()
  const selfBackup = useQuery(selfBackupQuery())
  const warnings = selfBackup.data?.warnings ?? []
  const tone = bannerTone(warnings)
  if (tone === null) return null
  const since = format.time(selfBackup.data?.overdueSince ?? null)
  const link = bannerLink(warnings)
  return (
    <Alert
      color={tones[tone]}
      title={t('selfBackup.bannerTitle')}
      mb="md"
      role="alert"
      data-no-print
    >
      <List size="sm">
        {warnings.map((warning) => (
          <List.Item key={warning}>{t(`selfBackup.warning.${warning}`, { since })}</List.Item>
        ))}
      </List>
      {link !== null && <AppLink to={link}>{t('selfBackup.openSettings')}</AppLink>}
    </Alert>
  )
}
