// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { onboardingQuery } from '../onboarding/api'
import { onboardingBanner } from '../onboarding/state'
import { tones } from '../theme'
import { AppLink } from './links'

// On an installation upgraded with agents and sources the console is as usual, but the server accepts no new agents
// until the self-backup is set up and the key copy confirmed (Рк16): a banner on every page leads to the wizard.
export function OnboardingBanner() {
  const { t } = useTranslation()
  const query = useQuery(onboardingQuery())
  const banner = query.data === undefined ? null : onboardingBanner(query.data)
  if (banner === null) return null
  return (
    <Alert
      color={tones.warning}
      title={t('onboardingBanner.title')}
      mb="md"
      role="alert"
      data-no-print
    >
      {t('onboardingBanner.message')} <AppLink to={banner.to}>{t('onboardingBanner.link')}</AppLink>
    </Alert>
  )
}
