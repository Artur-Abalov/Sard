// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import {
  Alert,
  Button,
  Group,
  NumberInput,
  Select,
  Stack,
  Switch,
  Text,
  TextInput,
} from '@mantine/core'
import { useDebouncedValue } from '@mantine/hooks'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, call, isRefusedValues } from '../api/call'
import { client } from '../api/client'
import { schedulePreviewQuery, scheduleQuery } from '../api/queries'
import type { components } from '../api/schema'
import { fieldFailures } from '../errors'
import {
  cronOf,
  draftOf,
  inputOf,
  PREVIEW_DEBOUNCE_MS,
  PRESET_KINDS,
  scheduleFieldTarget,
  WEEKDAYS,
  withKind,
  type Preset,
  type PresetKind,
  type ScheduleDraft,
  type Weekday,
} from '../schedule'
import { ErrorBlock } from './ErrorBlock'
import { AppLink } from './links'
import { SchedulePreview } from './SchedulePreview'
import { tones } from '../theme'

type Schedule = components['schemas']['Schedule']
type ScheduleInput = components['schemas']['ScheduleInput']

const MAX_MINUTE = 59

// The zones the browser knows, with UTC and the one already chosen, which a browser may not list.
function zonesWith(current: string | null): string[] {
  const zones = new Set(['UTC', ...Intl.supportedValuesOf('timeZone')])
  if (current !== null) zones.add(current)
  return [...zones].sort()
}

// The places of the editor the server named in a refusal (422) of a save or of a preview.
function refusedPlaces(error: unknown): Set<string> {
  if (!(error instanceof ApiError)) return new Set()
  return new Set(fieldFailures(error.body).map((failure) => scheduleFieldTarget(failure.field)))
}

function WeekdayField({
  preset,
  onChange,
}: {
  preset: Extract<Preset, { kind: 'weekly' }>
  onChange: (preset: Preset) => void
}) {
  const { t } = useTranslation()
  return (
    <Select
      label={t('schedule.weekday')}
      allowDeselect={false}
      data={WEEKDAYS.map((day) => ({
        value: String(day),
        label: t(`schedule.weekdayName.${day}`),
      }))}
      value={String(preset.day)}
      onChange={(value) => value !== null && onChange({ ...preset, day: Number(value) as Weekday })}
    />
  )
}

// The fields of the chosen variant: a time, a minute, a day of the week, or the cron itself.
function PresetFields({
  preset,
  onChange,
  error,
}: {
  preset: Preset
  onChange: (preset: Preset) => void
  error: string | undefined
}) {
  const { t } = useTranslation()
  if (preset.kind === 'cron') {
    return (
      <TextInput
        label={t('schedule.cron')}
        value={preset.text}
        error={error}
        onChange={(event) => onChange({ kind: 'cron', text: event.currentTarget.value })}
      />
    )
  }
  if (preset.kind === 'hourly') {
    return (
      <NumberInput
        label={t('schedule.minute')}
        min={0}
        max={MAX_MINUTE}
        allowDecimal={false}
        value={preset.minute}
        onChange={(value) => onChange({ kind: 'hourly', minute: Number(value) })}
      />
    )
  }
  return (
    <Group align="flex-end">
      {preset.kind === 'weekly' && <WeekdayField preset={preset} onChange={onChange} />}
      <TextInput
        type="time"
        label={t('schedule.time')}
        value={preset.time}
        error={error}
        onChange={(event) => onChange({ ...preset, time: event.currentTarget.value })}
      />
    </Group>
  )
}

// The picker of the variant: switching carries over the values that fit the new one.
function KindSelect({ preset, onChange }: { preset: Preset; onChange: (preset: Preset) => void }) {
  const { t } = useTranslation()
  return (
    <Select
      label={t('schedule.mode')}
      allowDeselect={false}
      data={PRESET_KINDS.map((kind) => ({ value: kind, label: t(`schedule.kind.${kind}`) }))}
      value={preset.kind}
      onChange={(kind) => kind !== null && onChange(withKind(preset, kind as PresetKind))}
    />
  )
}

// The two switches: whether the schedule fires, and whether successes are told too.
function Switches({
  draft,
  update,
}: {
  draft: ScheduleDraft
  update: (changes: Partial<ScheduleDraft>) => void
}) {
  const { t } = useTranslation()
  return (
    <>
      <Switch
        label={t('schedule.enabled')}
        checked={draft.enabled}
        onChange={(event) => update({ enabled: event.currentTarget.checked })}
      />
      <Switch
        label={t('schedule.notifyOnSuccess')}
        description={t('schedule.notifyHint')}
        checked={draft.notifyOnSuccess}
        onChange={(event) => update({ notifyOnSuccess: event.currentTarget.checked })}
      />
    </>
  )
}

// The zone of the schedule; until one is picked it is the server's, which the preview names.
function ZoneSelect({
  zone,
  error,
  onChange,
}: {
  zone: string | null
  error: string | undefined
  onChange: (timezone: string | null) => void
}) {
  const { t } = useTranslation()
  return (
    <Select
      label={t('schedule.timezone')}
      searchable
      allowDeselect={false}
      data={zonesWith(zone)}
      value={zone}
      error={error}
      onChange={onChange}
    />
  )
}

// The message for a field the server refused, if it did.
function refusal(refused: Set<string>, field: string, message: string): string | undefined {
  return refused.has(field) ? message : undefined
}

// The values of the schedule: variant, zone, the preview the server gives, the two switches.
function ScheduleFields({
  draft,
  update,
  preview,
  refused,
}: {
  draft: ScheduleDraft
  update: (changes: Partial<ScheduleDraft>) => void
  preview: Parameters<typeof SchedulePreview>[0]['query']
  refused: Set<string>
}) {
  const { t } = useTranslation()
  const zone = draft.timezone ?? preview.data?.timezone ?? null
  const cronError = refusal(refused, 'cron', t('schedule.error.cron'))
  const zoneError = refusal(refused, 'timezone', t('schedule.error.timezone'))
  return (
    <>
      <KindSelect preset={draft.preset} onChange={(preset) => update({ preset })} />
      <PresetFields
        preset={draft.preset}
        onChange={(preset) => update({ preset })}
        error={cronError}
      />
      <ZoneSelect zone={zone} error={zoneError} onChange={(timezone) => update({ timezone })} />
      {cronError === undefined && zoneError === undefined && <SchedulePreview query={preview} />}
      <Switches draft={draft} update={update} />
    </>
  )
}

// A refusal that every field owns is shown at the fields, not below them.
function ownedByFields(error: unknown): boolean {
  return isRefusedValues(error) && [...refusedPlaces(error)].every((place) => place !== 'form')
}

function isSourceGone(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    error.failure.kind === 'problem' &&
    error.failure.code === 'not_found'
  )
}

// A refusal of the save that no field owns; a source that is gone gets a way out.
function SaveError({ error }: { error: unknown }) {
  const { t } = useTranslation()
  if (error === null || error === undefined || ownedByFields(error)) return null
  if (isRefusedValues(error) || !isSourceGone(error)) return <ErrorBlock error={error} />
  return (
    <Alert color={tones.error} role="alert">
      <Text span>{t('schedule.sourceGone')}</Text>{' '}
      <AppLink to="/sources">{t('sources.title')}</AppLink>
    </Alert>
  )
}

// The editor of the schedule in the block of its card: Save sends one PUT, Cancel sends nothing.
export function ScheduleEditor({
  sourceId,
  schedule,
  onDone,
}: {
  sourceId: string
  schedule: Schedule | null
  onDone: () => void
}) {
  const { t, i18n } = useTranslation()
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState<ScheduleDraft>(() => draftOf(schedule))
  const [asked] = useDebouncedValue(
    { cron: cronOf(draft.preset), timezone: draft.timezone },
    PREVIEW_DEBOUNCE_MS,
  )
  const preview = useQuery({
    ...schedulePreviewQuery(asked.cron, asked.timezone, i18n.language),
    enabled: asked.cron !== '',
  })
  const input = inputOf(draft, preview.data?.timezone)
  const save = useMutation({
    mutationFn: (body: ScheduleInput) =>
      call(
        client.PUT('/api/v1/sources/{sourceId}/schedule', { params: { path: { sourceId } }, body }),
      ),
    onSuccess: (saved) => {
      queryClient.setQueryData(scheduleQuery(sourceId).queryKey, saved)
      void queryClient.invalidateQueries({ queryKey: ['schedule-fires', sourceId] })
      onDone()
    },
  })
  const refused = refusedPlaces(save.error ?? (preview.isError ? preview.error : null))
  return (
    <Stack>
      <ScheduleFields
        draft={draft}
        update={(changes) => setDraft((d) => ({ ...d, ...changes }))}
        preview={preview}
        refused={refused}
      />
      <SaveError error={save.error} />
      <Group>
        <Button
          loading={save.isPending}
          disabled={input === null}
          onClick={() => input !== null && save.mutate(input)}
        >
          {t('schedule.save')}
        </Button>
        <Button variant="default" onClick={onDone}>
          {t('common.cancel')}
        </Button>
      </Group>
    </Stack>
  )
}
