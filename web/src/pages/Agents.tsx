// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Stack, Table, Text, Title } from '@mantine/core'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { call } from '../api/call'
import { client } from '../api/client'
import { PAGE_SIZE } from '../api/queries'
import type { components } from '../api/schema'
import { AgentInstallBlock } from '../components/AgentInstallBlock'
import { AgentMarks } from '../components/AgentMarks'
import { EmptyState } from '../components/EmptyState'
import { AppLink, ButtonLink } from '../components/links'
import { PagedList } from '../components/PagedList'
import { StatusBadge } from '../components/StatusBadge'
import { usePaged } from '../components/usePaged'
import { EMPTY } from '../format'
import { tones } from '../theme'
import { Mono } from '../components/Mono'
import { Time } from '../components/Time'

const route = getRouteApi('/_app/agents/')

function AgentRow({ agent }: { agent: components['schemas']['AgentSummary'] }) {
  const { t } = useTranslation()
  return (
    <Table.Tr>
      <Table.Td>
        <AppLink to="/agents/$agentId" params={{ agentId: agent.id }}>
          <Mono>{agent.hostname}</Mono>
        </AppLink>
      </Table.Td>
      <Table.Td>
        <StatusBadge group="AgentStatus" value={agent.status} />
      </Table.Td>
      <Table.Td>
        {agent.lastSeenAt === null ? (
          <Text span c="dimmed">
            {t('agents.neverConnected')}
          </Text>
        ) : (
          <Time value={agent.lastSeenAt} />
        )}
      </Table.Td>
      <Table.Td>
        <Mono>{agent.agentVersion ?? EMPTY}</Mono>
      </Table.Td>
      <Table.Td>{agent.os === null ? EMPTY : `${agent.os} ${agent.arch ?? ''}`}</Table.Td>
      <Table.Td>
        <AgentMarks agent={agent} />
      </Table.Td>
    </Table.Tr>
  )
}

export function Agents() {
  const { t } = useTranslation()
  const { hint } = route.useSearch()
  const paged = usePaged(['agents', 'list'], (cursor) =>
    call(
      client.GET('/api/v1/agents', {
        params: { query: { limit: PAGE_SIZE, cursor: cursor ?? undefined } },
      }),
    ),
  )
  return (
    <Stack>
      <Title order={2}>{t('agents.title')}</Title>
      {hint === 'repo-init' && <Alert color={tones.info}>{t('agents.repoInitPage')}</Alert>}
      <PagedList
        paged={paged}
        empty={
          <>
            <EmptyState text={t('agents.empty')}>
              <ButtonLink to="/tokens" search={{ create: true }}>
                {t('dashboard.issueToken')}
              </ButtonLink>
            </EmptyState>
            <AgentInstallBlock open />
          </>
        }
      >
        {(agents) => (
          <>
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t('agents.host')}</Table.Th>
                  <Table.Th>{t('agents.status')}</Table.Th>
                  <Table.Th>{t('agents.lastSeen')}</Table.Th>
                  <Table.Th>{t('agents.version')}</Table.Th>
                  <Table.Th>{t('agents.os')}</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {agents.map((agent) => (
                  <AgentRow key={agent.id} agent={agent} />
                ))}
              </Table.Tbody>
            </Table>
            <AgentInstallBlock />
          </>
        )}
      </PagedList>
    </Stack>
  )
}
