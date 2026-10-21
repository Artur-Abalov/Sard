// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { repositoriesQuery } from '../api/queries'
import { bannerRepositoryCount } from '../recoveryKeyBanner'
import { tones } from '../theme'
import { AppLink } from './links'

// The repositories without a recovery key on every page (V3, КВ3). Without an answer (still loading, the
// request failed) there is nothing to say here and the page works as it does.
export function RecoveryKeyBanner() {
  const { t } = useTranslation()
  const repositories = useQuery(repositoriesQuery())
  const count = bannerRepositoryCount(repositories.data)
  if (count === null) return null
  return (
    <Alert color={tones.warning} mb="md" role="alert" data-no-print>
      {t('banners.noRecoveryKey', { count })}{' '}
      <AppLink to="/repositories">{t('repositories.title')}</AppLink>
    </Alert>
  )
}
