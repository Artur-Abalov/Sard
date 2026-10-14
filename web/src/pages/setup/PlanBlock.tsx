// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Group, List, Select, Stack, Text, TextInput, Title } from '@mantine/core'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { components } from '../../api/schema'
import { scheduleQuery } from '../../api/queries'
import { Loaded } from '../../components/Loaded'
import { Mono } from '../../components/Mono'
import { cronToTime } from '../../onboarding/schedule'
import { saveSchedule } from '../../onboarding/selfBackup'
import { tones } from '../../theme'

type SelfBackup = components['schemas']['SelfBackup']

// The zones the browser knows, with UTC and the one the schedule has already.
function zonesWith(current: string): string[] {
  return [...new Set(['UTC', ...Intl.supportedValuesOf('timeZone'), current])].sort()
}

// What goes into the backup, exactly as the server reports it, and the warning that the CA key is protected by the
// password of the repository alone (Рк11).
export function PlanBlock({ state }: { state: SelfBackup }) {
  const { t } = useTranslation()
  const database = state.sources.find((source) => source.role === 'self_database')
  return (
    <Stack>
      <Title order={4}>{t('setup.selfBackup.plan.title')}</Title>
      <Stack gap="xs">
        {state.plan.map((item) => (
          <Stack key={item.role} gap={2}>
            <Text fw={500}>
              {item.name} <Mono>{item.plugin}</Mono>
            </Text>
            <Text size="sm">{t('setup.selfBackup.plan.includes')}</Text>
            <Lines lines={item.includes} />
            {item.excludes.length > 0 && (
              <>
                <Text size="sm">{t('setup.selfBackup.plan.excludes')}</Text>
                <Lines lines={item.excludes} />
              </>
            )}
          </Stack>
        ))}
      </Stack>
      <Alert color={tones.warning}>{t('setup.selfBackup.plan.caWarning')}</Alert>
      {database && <ScheduleSection state={state} sourceId={database.sourceId} />}
    </Stack>
  )
}

function Lines({ lines }: { lines: string[] }) {
  return (
    <List size="sm">
      {lines.map((line) => (
        <List.Item key={line}>
          <Mono>{line}</Mono>
        </List.Item>
      ))}
    </List>
  )
}

// The schedule of the two system sources starts from the database's: a time of day and a zone, saved as one cron
// "M H * * *" for both (Рк12). A cron that is not a time of day is shown as it is, without a field for the time.
function ScheduleSection({ state, sourceId }: { state: SelfBackup; sourceId: string }) {
  const { t } = useTranslation()
  const query = useQuery(scheduleQuery(sourceId))
  return (
    <Stack>
      <Title order={4}>{t('setup.selfBackup.schedule.title')}</Title>
      <Loaded query={query}>
        {(schedule) =>
          schedule === null ? null : <ScheduleForm state={state} schedule={schedule} />
        }
      </Loaded>
    </Stack>
  )
}

function ScheduleForm({
  state,
  schedule,
}: {
  state: SelfBackup
  schedule: components['schemas']['Schedule']
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const initialTime = cronToTime(schedule.cron)
  const [time, setTime] = useState(initialTime ?? '')
  const [zone, setZone] = useState(schedule.timezone)
  const [saved, setSaved] = useState<'idle' | 'saved' | 'failed'>('idle')

  async function save() {
    const ok = await saveSchedule(state, time, zone)
    setSaved(ok ? 'saved' : 'failed')
    if (ok) await queryClient.invalidateQueries({ queryKey: ['schedule'] })
  }

  if (initialTime === null) {
    return <Text>{t('setup.selfBackup.schedule.custom', { cron: schedule.cron })}</Text>
  }
  return (
    <Stack>
      <Group align="flex-end">
        <TextInput
          type="time"
          label={t('setup.selfBackup.schedule.time')}
          value={time}
          onChange={(event) => setTime(event.currentTarget.value)}
        />
        <Select
          label={t('setup.selfBackup.schedule.timezone')}
          searchable
          allowDeselect={false}
          value={zone}
          data={zonesWith(schedule.timezone)}
          onChange={(value) => setZone(value ?? schedule.timezone)}
        />
        <Button variant="default" disabled={time === ''} onClick={() => void save()}>
          {t('setup.selfBackup.schedule.save')}
        </Button>
      </Group>
      {saved === 'saved' && (
        <Text size="sm" role="status">
          {t('setup.selfBackup.schedule.saved')}
        </Text>
      )}
      {saved === 'failed' && (
        <Text size="sm" c={tones.error} role="alert">
          {t('setup.selfBackup.schedule.failed')}
        </Text>
      )}
    </Stack>
  )
}
