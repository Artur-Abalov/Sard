// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Stack } from '@mantine/core'
import { useRouter } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

// Shown when a protected page's session check fails for a reason other than "no
// session" (network, 5xx): the URL does not change and a retry re-runs beforeLoad
// (Р9в). Route-level errors, not a page's own data, so this stays out of the way
// of the ordinary component-test ban: it renders nothing conditional on data.
export function RouteError() {
  const { t } = useTranslation()
  const router = useRouter()
  return (
    <Stack align="center" justify="center" mih="100vh">
      <Alert color="red" title={t('routeError.title')}>
        {t('routeError.message')}
      </Alert>
      <Button onClick={() => void router.invalidate()}>{t('routeError.retry')}</Button>
    </Stack>
  )
}
