// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Group, Stack, Table, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { call } from '../api/call'
import { client } from '../api/client'
import { firstAgentQuery, PAGE_SIZE, type Source } from '../api/queries'
import { AgentName } from '../components/AgentName'
import { EmptyState } from '../components/EmptyState'
import { AppLink, ButtonLink } from '../components/links'
import { PagedList } from '../components/PagedList'
import { RunBackupButton } from '../components/RunBackupButton'
import { usePaged } from '../components/usePaged'
import { tones } from '../theme'

const route = getRouteApi('/_app/sources/')

// With an agent the next step is a source; without one it is a token (the agent comes first).
function NoSources() {
  const { t } = useTranslation()
  const agents = useQuery(firstAgentQuery())
  if (agents.data === undefined) return null
  if (agents.data.items.length > 0) {
    return (
      <EmptyState text={t('sources.empty')}>
        <ButtonLink to="/sources/new">{t('sources.create')}</ButtonLink>
      </EmptyState>
    )
  }
  return (
    <EmptyState text={t('sources.needAgent')}>
      <ButtonLink to="/tokens" search={{ create: true }}>
        {t('dashboard.issueToken')}
      </ButtonLink>
    </EmptyState>
  )
}

function SourceRow({ source }: { source: Source }) {
  return (
    <Table.Tr>
      <Table.Td>
        <AppLink to="/sources/$sourceId" params={{ sourceId: source.id }}>
          {source.name}
        </AppLink>
      </Table.Td>
      <Table.Td>
        <AgentName agentId={source.agentId} />
      </Table.Td>
      <Table.Td>{source.plugin}</Table.Td>
      <Table.Td>{source.repositoryName}</Table.Td>
      <Table.Td>
        <RunBackupButton sourceId={source.id} />
      </Table.Td>
    </Table.Tr>
  )
}

export function Sources() {
  const { t } = useTranslation()
  const { hint } = route.useSearch()
  const paged = usePaged(['sources', 'list'], (cursor) =>
    call(
      client.GET('/api/v1/sources', {
        params: { query: { limit: PAGE_SIZE, cursor: cursor ?? undefined } },
      }),
    ),
  )
  return (
    <Stack>
      <Group justify="space-between">
        <Title order={2}>{t('sources.title')}</Title>
        <ButtonLink to="/sources/new">{t('sources.create')}</ButtonLink>
      </Group>
      {hint === 'run-backup' && <Alert color={tones.info}>{t('sources.runBackupHint')}</Alert>}
      <PagedList paged={paged} empty={<NoSources />}>
        {(sources) => (
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t('sources.name')}</Table.Th>
                <Table.Th>{t('sources.agent')}</Table.Th>
                <Table.Th>{t('sources.plugin')}</Table.Th>
                <Table.Th>{t('sources.repository')}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {sources.map((source) => (
                <SourceRow key={source.id} source={source} />
              ))}
            </Table.Tbody>
          </Table>
        )}
      </PagedList>
    </Stack>
  )
}
