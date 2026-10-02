// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge, Table } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { components } from '../api/schema'
import { useFormat } from '../useFormat'
import { AppLink } from './links'
import { StatusBadge } from './StatusBadge'

type RunSummary = components['schemas']['RunSummary']

// The source of a run by the name the server sent: linked while it exists, marked when it was deleted.
export function SourceLabel({
  run,
}: {
  run: Pick<RunSummary, 'sourceId' | 'sourceName' | 'sourceDeleted'>
}) {
  const { t } = useTranslation()
  if (run.sourceDeleted) {
    return (
      <>
        {run.sourceName} <Badge color="gray">{t('runs.sourceDeleted')}</Badge>
      </>
    )
  }
  return (
    <AppLink to="/sources/$sourceId" params={{ sourceId: run.sourceId }}>
      {run.sourceName}
    </AppLink>
  )
}

// Runs in the order the server gave them: source, status, when queued, a link to the card.
export function RunsTable({ runs }: { runs: RunSummary[] }) {
  const { t } = useTranslation()
  const format = useFormat()
  return (
    <Table>
      <Table.Thead>
        <Table.Tr>
          <Table.Th>{t('runs.source')}</Table.Th>
          <Table.Th>{t('runs.status')}</Table.Th>
          <Table.Th>{t('runs.queuedAt')}</Table.Th>
          <Table.Th />
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {runs.map((run) => (
          <Table.Tr key={run.id}>
            <Table.Td>
              <SourceLabel run={run} />
            </Table.Td>
            <Table.Td>
              <StatusBadge group="RunStatus" value={run.status} />
            </Table.Td>
            <Table.Td>{format.time(run.queuedAt)}</Table.Td>
            <Table.Td>
              <AppLink to="/runs/$runId" params={{ runId: run.id }}>
                {t('runs.open')}
              </AppLink>
            </Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  )
}
