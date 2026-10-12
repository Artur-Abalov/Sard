// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge, Group, Stack, Table, Text, Title } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { call } from '../api/call'
import { client } from '../api/client'
import { PAGE_SIZE } from '../api/queries'
import type { components } from '../api/schema'
import { EmptyState } from './EmptyState'
import { AppLink } from './links'
import { PagedList } from './PagedList'
import { usePaged } from './usePaged'
import { tones } from '../theme'
import { useFormat } from '../useFormat'

type Fire = components['schemas']['ScheduleFire']

// The key of the words for an outcome, with its reason where the outcome has one.
function outcomeKey(fire: Fire): string {
  const base = `schedule.journal.outcome.${fire.outcome}`
  return fire.reason === null ? base : `${base}.${fire.reason}`
}

// What came of a fire: the words for the outcome, a downtime as the period it missed.
function FireText({ fire, timezone }: { fire: Fire; timezone: string }) {
  const { t } = useTranslation()
  const format = useFormat()
  if (fire.outcome === 'skipped_downtime') {
    return (
      <Text span>
        {format.missed({
          from: fire.scheduledFor,
          until: fire.missedUntil ?? fire.scheduledFor,
          count: fire.missedCount ?? 0,
          capped: fire.missedCountCapped,
          timezone,
        })}
      </Text>
    )
  }
  return <Text span>{t(outcomeKey(fire))}</Text>
}

function FireRow({ fire, timezone }: { fire: Fire; timezone: string }) {
  const { t } = useTranslation()
  const format = useFormat()
  return (
    <Table.Tr>
      <Table.Td>{format.scheduleTime(fire.scheduledFor, timezone)}</Table.Td>
      <Table.Td>
        <Group gap="xs">
          <FireText fire={fire} timezone={timezone} />
          {fire.kind === 'catch_up' && (
            <Badge color={tones.neutral}>{t('schedule.journal.catchUp')}</Badge>
          )}
          {fire.alert && (
            <Badge color={tones.warning} title={t('schedule.journal.alerted')}>
              {t('schedule.journal.alerted')}
            </Badge>
          )}
        </Group>
      </Table.Td>
      <Table.Td>
        {fire.runId !== null && (
          <AppLink to="/runs/$runId" params={{ runId: fire.runId }}>
            {t('runs.open')}
          </AppLink>
        )}
      </Table.Td>
    </Table.Tr>
  )
}

// The journal of the schedule in the order the server gives it, a page at a time; a failure here
// leaves the rest of the card as it is.
export function ScheduleJournal({ sourceId, timezone }: { sourceId: string; timezone: string }) {
  const { t } = useTranslation()
  const paged = usePaged(['schedule-fires', sourceId], (cursor) =>
    call(
      client.GET('/api/v1/sources/{sourceId}/schedule/fires', {
        params: {
          path: { sourceId },
          query: { limit: PAGE_SIZE, cursor: cursor ?? undefined },
        },
      }),
    ),
  )
  return (
    <Stack gap="xs">
      <Title order={5}>{t('schedule.journal.title')}</Title>
      <PagedList paged={paged} empty={<EmptyState text={t('schedule.journal.empty')} />}>
        {(fires) => (
          <Table>
            <Table.Tbody>
              {fires.map((fire) => (
                <FireRow key={fire.id} fire={fire} timezone={timezone} />
              ))}
            </Table.Tbody>
          </Table>
        )}
      </PagedList>
    </Stack>
  )
}
