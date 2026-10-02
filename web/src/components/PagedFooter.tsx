// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Button } from '@mantine/core'
import { useTranslation } from 'react-i18next'

// "Show more" under a paged list; absent on the last page.
export function PagedFooter({
  hasNext,
  fetching,
  onMore,
}: {
  hasNext: boolean
  fetching: boolean
  onMore: () => void
}) {
  const { t } = useTranslation()
  if (!hasNext) return null
  return (
    <Button variant="default" loading={fetching} onClick={onMore}>
      {t('common.showMore')}
    </Button>
  )
}
