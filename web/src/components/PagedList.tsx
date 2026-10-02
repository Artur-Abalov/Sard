// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Loader } from '@mantine/core'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { ErrorBlock } from './ErrorBlock'
import { PagedFooter } from './PagedFooter'
import type { Paged } from './usePaged'

// A paged list's states: loading, the error with a retry, the empty state, or the items in the
// order of the server's answers with "Show more" while there is a next page. A failed refresh
// keeps the items and says they are not being updated.
export function PagedList<T>({
  paged,
  empty,
  children,
}: {
  paged: Paged<T>
  empty: ReactNode
  children: (items: T[]) => ReactNode
}) {
  const { t } = useTranslation()
  const { query, items } = paged
  if (items === undefined) {
    if (query.isError)
      return <ErrorBlock error={query.error} onRetry={() => void query.refetch()} />
    return <Loader size="sm" aria-label={t('common.loading')} />
  }
  return (
    <>
      {query.isError && <Alert color="yellow">{t('common.notUpdating')}</Alert>}
      {items.length === 0 ? empty : children(items)}
      <PagedFooter
        hasNext={query.hasNextPage}
        fetching={query.isFetchingNextPage}
        onMore={() => void query.fetchNextPage()}
      />
    </>
  )
}
