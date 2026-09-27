// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Anchor, Stack, Title } from '@mantine/core'
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

export function NotFound() {
  const { t } = useTranslation()
  return (
    <Stack p="md">
      <Title order={2}>{t('notFound.title')}</Title>
      <Anchor renderRoot={(props) => <Link to="/" {...props} />}>{t('notFound.home')}</Anchor>
    </Stack>
  )
}
