// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge, Table, Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { components } from '../api/schema'
import { AppLink } from './links'
import { StatusBadge } from './StatusBadge'
import { tones } from '../theme'
import { Mono } from './Mono'
import { Time } from './Time'
import { useFormat } from '../useFormat'

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
        <Mono>{run.sourceName}</Mono> <Badge color={tones.neutral}>{t('runs.sourceDeleted')}</Badge>
      </>
    )
  }
  return (
    <AppLink to="/sources/$sourceId" params={{ sourceId: run.sourceId }}>
      <Mono>{run.sourceName}</Mono>
    </AppLink>
  )
}

// What started a run; a catch-up run says which fires of a server downtime it stands for.
export function TriggerLabel({ run }: { run: Pick<RunSummary, 'trigger' | 'catchUp'> }) {
  const { t } = useTranslation()
  const format = useFormat()
  const { catchUp } = run
  if (catchUp === null) return <Text span>{t(`enum.RunTrigger.${run.trigger}`)}</Text>
  return (
    <Text span>
      {format.catchUp({
        from: catchUp.missedFrom,
        until: catchUp.missedUntil,
        count: catchUp.missedCount,
        capped: catchUp.missedCountCapped,
        timezone: catchUp.timezone,
      })}
    </Text>
  )
}

// Runs in the order the server gave them: source, status, what started it, when queued, a link to the card.
export function RunsTable({ runs }: { runs: RunSummary[] }) {
  const { t } = useTranslation()
  return (
    <Table>
      <Table.Thead>
        <Table.Tr>
          <Table.Th>{t('runs.source')}</Table.Th>
          <Table.Th>{t('runs.status')}</Table.Th>
          <Table.Th>{t('runs.startedBy')}</Table.Th>
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
            <Table.Td>
              <TriggerLabel run={run} />
            </Table.Td>
            <Table.Td>
              <Time value={run.queuedAt} />
            </Table.Td>
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
