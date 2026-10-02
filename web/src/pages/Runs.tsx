// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Group, MultiSelect, Select, Stack, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { call } from '../api/call'
import { client } from '../api/client'
import { PAGE_SIZE, sourcePickerQuery } from '../api/queries'
import type { components } from '../api/schema'
import { EmptyState } from '../components/EmptyState'
import { AppLink, ButtonLink } from '../components/links'
import { PagedList } from '../components/PagedList'
import { RunsTable } from '../components/RunsTable'
import { usePaged } from '../components/usePaged'
import { runListPollInterval } from '../polling'
import { RUN_STATUSES, type RunFilter } from '../runFilter'

const route = getRouteApi('/_app/runs/')

type RunSummary = components['schemas']['RunSummary']

// The sources to choose from, and the filtered source itself when the address names one that is
// gone: it is then called by the name its runs carry, marked deleted.
function useSourceOptions(filter: RunFilter, runs: RunSummary[] | undefined) {
  const { t } = useTranslation()
  const sources = useQuery(sourcePickerQuery())
  const options = (sources.data?.items ?? []).map((s) => ({ value: s.id, label: s.name }))
  const missing = filter.sourceId !== null && !options.some((o) => o.value === filter.sourceId)
  const run = runs?.find((r) => r.sourceId === filter.sourceId)
  if (missing && run) {
    options.push({ value: run.sourceId, label: `${run.sourceName} (${t('runs.sourceDeleted')})` })
  }
  return options
}

function Filters({ filter, runs }: { filter: RunFilter; runs: RunSummary[] | undefined }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const options = useSourceOptions(filter, runs)
  const go = (next: RunFilter) =>
    void navigate({
      to: '/runs',
      search: {
        ...(next.statuses.length > 0 ? { status: next.statuses } : {}),
        ...(next.sourceId === null ? {} : { sourceId: next.sourceId }),
      },
    })
  return (
    <Group align="flex-end">
      <Select
        w={260}
        label={t('runs.source')}
        clearable
        data={options}
        value={filter.sourceId}
        onChange={(sourceId) => go({ ...filter, sourceId })}
      />
      <MultiSelect
        w={360}
        label={t('runs.status')}
        clearable
        data={RUN_STATUSES.map((value) => ({ value, label: t(`enum.RunStatus.${value}`) }))}
        value={filter.statuses}
        onChange={(values) => go({ ...filter, statuses: values as RunFilter['statuses'] })}
      />
    </Group>
  )
}

// Nothing under a filter is not the same as no runs at all.
function NoRuns({ filtered }: { filtered: boolean }) {
  const { t } = useTranslation()
  if (filtered) {
    return (
      <EmptyState text={t('runs.noMatch')}>
        <ButtonLink to="/runs" search={{}} variant="default">
          {t('runs.resetFilters')}
        </ButtonLink>
      </EmptyState>
    )
  }
  return (
    <EmptyState text={t('runs.empty')}>
      <AppLink to="/sources">{t('sources.title')}</AppLink>
    </EmptyState>
  )
}

export function Runs() {
  const { t } = useTranslation()
  const search = route.useSearch()
  const filter: RunFilter = { statuses: search.status ?? [], sourceId: search.sourceId ?? null }
  const paged = usePaged(
    ['runs', 'list', filter],
    (cursor) =>
      call(
        client.GET('/api/v1/runs', {
          params: {
            query: {
              status: filter.statuses.length > 0 ? filter.statuses : undefined,
              sourceId: filter.sourceId ?? undefined,
              limit: PAGE_SIZE,
              cursor: cursor ?? undefined,
            },
          },
        }),
      ),
    runListPollInterval,
  )
  const filtered = filter.statuses.length > 0 || filter.sourceId !== null
  return (
    <Stack>
      <Title order={2}>{t('runs.title')}</Title>
      <Filters filter={filter} runs={paged.items} />
      <PagedList paged={paged} empty={<NoRuns filtered={filtered} />}>
        {(runs) => <RunsTable runs={runs} />}
      </PagedList>
    </Stack>
  )
}
