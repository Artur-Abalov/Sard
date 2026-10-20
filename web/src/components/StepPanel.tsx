// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Badge, Card, Code, Group, Progress, Stack, Text, Title } from '@mantine/core'
import type { ReactNode } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import type { RunStep } from '../api/queries'
import type { components } from '../api/schema'
import {
  formatPercent,
  formatRestoreResult,
  formatRolesRestored,
  formatTablesRestored,
} from '../format'
import { stepPollInterval } from '../polling'
import { partialNoteShown, stepNoteShown } from '../stepNotes'
import { useFormat } from '../useFormat'
import { Mono } from './Mono'
import { StatusBadge } from './StatusBadge'
import { StepLogView } from './StepLogView'
import { tones } from '../theme'

type Schemas = components['schemas']

// The reason the server gave, as it gave it, under the words that say what kind of end it was.
function Reason({ step }: { step: RunStep }) {
  const { t } = useTranslation()
  const explained = stepNoteShown(step.status)
  if (!explained && step.message === null) return null
  return (
    <Alert color={tones.error}>
      {explained && <Text size="sm">{t(`run.stepNote.${step.status}`)}</Text>}
      {step.message !== null && <Text>{step.message}</Text>}
    </Alert>
  )
}

// Bytes, percent and files of the step so far; the percent only while the total is known.
function Progress_({ step }: { step: RunStep }) {
  const format = useFormat()
  const percent = formatPercent(step.bytesProcessed, step.bytesTotal)
  const files = format.files(step.filesProcessed, step.filesTotal)
  if (step.bytesProcessed === null && files === null) return null
  return (
    <Stack gap={4}>
      {step.bytesProcessed !== null && (
        <Text>
          {step.bytesTotal === null ? (
            <Mono>{format.bytes(step.bytesProcessed)}</Mono>
          ) : (
            <Trans
              i18nKey="run.bytesOf"
              values={{
                processed: format.bytes(step.bytesProcessed),
                total: format.bytes(step.bytesTotal),
              }}
              components={{ m: <Mono /> }}
            />
          )}
          {percent !== null && <Mono>{` — ${percent}%`}</Mono>}
        </Text>
      )}
      {percent !== null && <Progress value={percent} aria-label={`${percent}%`} />}
      {files !== null && <Text size="sm">{files}</Text>}
    </Stack>
  )
}

// The snapshot a backup step made, with the sizes; incomplete when the server says so.
function BackupBlock({ step }: { step: RunStep }) {
  const { t } = useTranslation()
  const format = useFormat()
  const { backup } = step
  if (backup === null) return null
  return (
    <Card withBorder>
      <Title order={5}>{t('run.snapshot')}</Title>
      <Group>
        <Code>{backup.snapshotId}</Code>
        <Text size="sm">
          <Trans
            i18nKey="run.repository"
            values={{ name: step.repositoryName ?? '' }}
            components={{ m: <Mono /> }}
          />
        </Text>
        <Text size="sm">
          <Trans
            i18nKey="run.total"
            values={{ size: format.bytes(backup.totalBytes) }}
            components={{ m: <Mono /> }}
          />
        </Text>
        <Text size="sm">
          <Trans
            i18nKey="run.added"
            values={{ size: format.bytes(backup.addedBytes) }}
            components={{ m: <Mono /> }}
          />
        </Text>
      </Group>
      {partialNoteShown(step) && (
        <Alert color={tones.warning} mt="xs">
          {t('run.partialNote')}
        </Alert>
      )}
    </Card>
  )
}

// A restore of a database: its name, its tables and, when it brought global objects, its roles.
function RestoredDatabase({ restore }: { restore: Schemas['RestoreOutput'] }) {
  const { i18n } = useTranslation()
  const roles = restore.rolesRestored ?? 0
  return (
    <Alert color={tones.success} role="status">
      <Text>
        <Mono>{restore.database ?? ''}</Mono>
        {' — '}
        {formatTablesRestored(restore.tablesRestored ?? 0, i18n.language)}
        {roles > 0 && `, ${formatRolesRestored(roles, i18n.language)}`}
      </Text>
    </Alert>
  )
}

function Restored({ restore }: { restore: Schemas['RestoreOutput'] }) {
  const { t, i18n } = useTranslation()
  if (restore.database !== null) return <RestoredDatabase restore={restore} />
  return (
    <Alert color={tones.success} role="status">
      <Text>
        {formatRestoreResult(restore.filesRestored, restore.bytesRestored, i18n.language)}
        {' — '}
        <Mono>{restore.target ?? ''}</Mono>
      </Text>
      {restore.ownersRestored === false && <Text size="sm">{t('restore.ownersNotRestored')}</Text>}
    </Alert>
  )
}

function RollbackFailed({ leftover }: { leftover: string | null }) {
  return (
    <Alert color={tones.error} role="alert">
      <Trans
        i18nKey="restore.rollbackFailed"
        values={{ leftover: leftover ?? '' }}
        components={{ m: <Mono /> }}
      />
    </Alert>
  )
}

function LateSuccess({ target }: { target: string | null }) {
  return (
    <Alert color={tones.warning} role="status">
      <Trans
        i18nKey="restore.lateSuccess"
        values={{ target: target ?? '' }}
        components={{ m: <Mono /> }}
      />
    </Alert>
  )
}

function Nothing() {
  const { t } = useTranslation()
  return <Alert color={tones.neutral}>{t('restore.nothingRestored')}</Alert>
}

type Restore = Schemas['RestoreOutput'] | null
type Outcome = NonNullable<RunStep['restoreOutcome']>

const OUTCOMES: Record<Outcome, (restore: Restore) => ReactNode> = {
  restored: (restore) => restore && <Restored restore={restore} />,
  nothing: () => <Nothing />,
  rollback_failed: (restore) => <RollbackFailed leftover={restore?.leftover ?? null} />,
  late: (restore) => <LateSuccess target={restore?.target ?? null} />,
}

// How a restore step ended, in the words of the server's outcome: what it wrote, that nothing was
// restored, what its rollback left behind, or where a lost step's late result put the data.
function RestoreBlock({ step }: { step: RunStep }) {
  return step.restoreOutcome === null ? null : OUTCOMES[step.restoreOutcome](step.restore)
}

export function StepPanel({ runId, step }: { runId: string; step: RunStep }) {
  const { t } = useTranslation()
  const format = useFormat()
  return (
    <Card withBorder>
      <Stack>
        <Group>
          <Title order={4}>{t(`enum.StepAction.${step.action}`)}</Title>
          <StatusBadge group="StepStatus" value={step.status} />
          {step.phase !== null && (
            <Badge variant="outline">{t(`enum.StepPhase.${step.phase}`)}</Badge>
          )}
          <Text size="sm">
            <Trans
              i18nKey="run.duration"
              values={{ value: format.duration(step.startedAt, step.finishedAt) }}
              components={{ m: <Mono /> }}
            />
          </Text>
        </Group>
        <Progress_ step={step} />
        <Reason step={step} />
        <BackupBlock step={step} />
        <RestoreBlock step={step} />
        <Title order={5}>{t('run.log')}</Title>
        <StepLogView
          runId={runId}
          stepId={step.id}
          active={stepPollInterval(step.status) !== false}
        />
      </Stack>
    </Card>
  )
}
