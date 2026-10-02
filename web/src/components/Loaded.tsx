// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Loader } from '@mantine/core'
import type { UseQueryResult } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { ErrorBlock } from './ErrorBlock'
import { ErrorOrNotFound } from './ErrorOrNotFound'
import { tones } from '../theme'

// A query's states: loading, an error with a retry, or its data. When a refresh fails
// the data stays on the page and a note says it is not being updated.
export function Loaded<T>({
  query,
  back,
  children,
}: {
  query: Pick<UseQueryResult<T>, 'data' | 'error' | 'isPending' | 'isError' | 'refetch'>
  // For a card: the link back to its list, shown with "not found" when the object is gone.
  back?: ReactNode
  children: (data: T) => ReactNode
}) {
  const { t } = useTranslation()
  if (query.data !== undefined) {
    return (
      <>
        {query.isError && <Alert color={tones.warning}>{t('common.notUpdating')}</Alert>}
        {children(query.data)}
      </>
    )
  }
  if (query.isPending) return <Loader size="sm" aria-label={t('common.loading')} />
  const retry = () => void query.refetch()
  if (back) return <ErrorOrNotFound error={query.error} onRetry={retry} back={back} />
  return <ErrorBlock error={query.error} onRetry={retry} />
}
