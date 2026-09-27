// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Title } from '@mantine/core'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

const route = getRouteApi('/_app/runs/$runId')

export function Run() {
  const { t } = useTranslation()
  const { runId } = route.useParams()
  return <Title order={2}>{t('run.title', { runId })}</Title>
}
