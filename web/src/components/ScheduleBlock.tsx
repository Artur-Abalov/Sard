// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Badge, Button, Card, Group, Stack, Table, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { schedulePreviewQuery, scheduleQuery } from '../api/queries'
import type { components } from '../api/schema'
import { Loaded } from './Loaded'
import { AppLink } from './links'
import { ScheduleEditor } from './ScheduleEditor'
import { ScheduleJournal } from './ScheduleJournal'
import { FrequencyWarning } from './SchedulePreview'
import { StatusBadge } from './StatusBadge'
import { Time } from './Time'
import { tones } from '../theme'
import { useFormat } from '../useFormat'

type Schedule = components['schemas']['Schedule']

// The last run the schedule created and what came of it, or that there has been none.
function LastRun({ schedule }: { schedule: Schedule }) {
  const { t } = useTranslation()
  const { lastRun } = schedule
  if (lastRun === null) return <Text c="dimmed">{t('schedule.noRuns')}</Text>
  return (
    <Group gap="xs">
      <StatusBadge group="RunStatus" value={lastRun.status} />
      <Text span>{t(`enum.RunTrigger.${lastRun.trigger}`)}</Text>
      <AppLink to="/runs/$runId" params={{ runId: lastRun.id }}>
        {t('runs.open')}
      </AppLink>
    </Group>
  )
}

// The saved schedule in words, with the zone, the next run and the last one, and what is amiss.
function ScheduleSummary({ schedule }: { schedule: Schedule }) {
  const { t, i18n } = useTranslation()
  const format = useFormat()
  const preview = useQuery(schedulePreviewQuery(schedule.cron, schedule.timezone, i18n.language))
  return (
    <Stack gap="xs">
      <Group gap="xs">
        <Text fw={600}>{preview.data?.description ?? schedule.cron}</Text>
        {!schedule.enabled && <Badge color={tones.neutral}>{t('schedule.off')}</Badge>}
      </Group>
      <FrequencyWarning preview={preview.data} />
      <Table>
        <Table.Tbody>
          <Table.Tr>
            <Table.Th>{t('schedule.timezone')}</Table.Th>
            <Table.Td>{schedule.timezone}</Table.Td>
          </Table.Tr>
          {schedule.nextRunAt !== null && (
            <Table.Tr>
              <Table.Th>{t('schedule.next')}</Table.Th>
              <Table.Td>
                {format.scheduleTime(schedule.nextRunAt, schedule.timezone)}{' '}
                <Time value={schedule.nextRunAt} />
              </Table.Td>
            </Table.Tr>
          )}
          <Table.Tr>
            <Table.Th>{t('schedule.last')}</Table.Th>
            <Table.Td>
              <LastRun schedule={schedule} />
            </Table.Td>
          </Table.Tr>
        </Table.Tbody>
      </Table>
      {schedule.catchUpAt !== null && (
        <Alert color={tones.info} role="status">
          {t('schedule.catchUpPending', {
            time: format.scheduleTime(schedule.catchUpAt, schedule.timezone),
          })}
        </Alert>
      )}
      {schedule.skippedInRow > 0 && (
        <Alert color={tones.warning} role="status">
          {t('schedule.skippedInRow', { count: schedule.skippedInRow })}
        </Alert>
      )}
      {schedule.notifyOnSuccess && <Text size="sm">{t('schedule.notifyOn')}</Text>}
    </Stack>
  )
}

// The block "Schedule" of a source's card: the schedule and its journal, with an editor in place.
// A failure to load it leaves the rest of the card as it is.
export function ScheduleBlock({ sourceId }: { sourceId: string }) {
  const { t } = useTranslation()
  const schedule = useQuery(scheduleQuery(sourceId))
  const [editing, setEditing] = useState(false)
  return (
    <Card withBorder>
      <Stack>
        <Group justify="space-between">
          <Title order={4}>{t('schedule.title')}</Title>
          {schedule.data !== undefined && !editing && (
            <Button variant="default" onClick={() => setEditing(true)}>
              {t(schedule.data === null ? 'schedule.set' : 'schedule.change')}
            </Button>
          )}
        </Group>
        <Loaded query={schedule}>
          {(data) =>
            editing ? (
              <ScheduleEditor
                sourceId={sourceId}
                schedule={data}
                onDone={() => setEditing(false)}
              />
            ) : data === null ? (
              <Text c="dimmed">{t('schedule.none')}</Text>
            ) : (
              <>
                <ScheduleSummary schedule={data} />
                <ScheduleJournal sourceId={sourceId} timezone={data.timezone} />
              </>
            )
          }
        </Loaded>
      </Stack>
    </Card>
  )
}
