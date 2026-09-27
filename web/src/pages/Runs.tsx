// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Title } from '@mantine/core'
import { useTranslation } from 'react-i18next'

export function Runs() {
  const { t } = useTranslation()
  return <Title order={2}>{t('runs.title')}</Title>
}
