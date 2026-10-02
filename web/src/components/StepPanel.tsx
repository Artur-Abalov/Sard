// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Badge, Card, Code, Group, Progress, Stack, Text, Title } from '@mantine/core'
import { Trans, useTranslation } from 'react-i18next'
import type { RunStep } from '../api/queries'
import { formatPercent } from '../format'
import { stepPollInterval } from '../polling'
import { partialNoteShown, stepNoteShown } from '../stepNotes'
import { useFormat } from '../useFormat'
import { Mono } from './Mono'
import { StatusBadge } from './StatusBadge'
import { StepLogView } from './StepLogView'
import { tones } from '../theme'

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
