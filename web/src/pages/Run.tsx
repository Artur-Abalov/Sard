// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Badge, Code, Group, Stack, Table, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { runQuery, type Run as RunModel } from '../api/queries'
import { AgentName } from '../components/AgentName'
import { CancelRunButton } from '../components/CancelRunButton'
import { OutcomeBadge } from '../components/OutcomeBadge'
import { AppLink, ButtonLink } from '../components/links'
import { Loaded } from '../components/Loaded'
import { RestoreButton } from '../components/RestoreButton'
import { RunBackupButton } from '../components/RunBackupButton'
import { SourceLabel, TriggerLabel } from '../components/RunsTable'
import { StepPanel } from '../components/StepPanel'
import { restoreSearchOf } from '../restoreView'
import { timeoutOfRun } from '../runOutcome'
import { tones } from '../theme'
import { Mono } from '../components/Mono'
import { Time } from '../components/Time'
import { useFormat } from '../useFormat'

const route = getRouteApi('/_app/runs/$runId')

// The wait for an offline agent, with the deadline the server set.
function WaitNote({ run }: { run: RunModel }) {
  const { t } = useTranslation()
  if (run.outcome !== 'waiting_for_agent') return null
  return (
    <Alert color={tones.info} role="status">
      {t('run.waitingUntil')} <Time value={run.agentWaitUntil} />{' '}
      <AgentName agentId={run.agentId} />
    </Alert>
  )
}

// What to check, and where to raise the timeout, when a step ran over it.
function TimedOutNote({ run }: { run: RunModel }) {
  const { t } = useTranslation()
  if (run.outcome !== 'timed_out') return null
  return (
    <Alert color={tones.warning} role="status">
      <Text>{t('run.timedOut.storage')}</Text>
      <Text>
        {t('run.timedOut.increase')}{' '}
        {!run.sourceDeleted && (
          <AppLink to="/sources/$sourceId/edit" params={{ sourceId: run.sourceId }}>
            {t('run.timedOut.openSource')}
          </AppLink>
        )}
      </Text>
    </Alert>
  )
}

// Since when the agent was out of contact, or that it never connected.
function UnavailableNote({ run }: { run: RunModel }) {
  const { t } = useTranslation()
  const format = useFormat()
  if (run.outcome !== 'agent_unavailable') return null
  return (
    <Alert color={tones.info} role="status">
      {run.agentUnavailableSince === null
        ? t('run.agentNeverConnected')
        : t('run.agentUnavailableSince', { time: format.time(run.agentUnavailableSince) })}
    </Alert>
  )
}

// What the agent reported after the server had closed the step as cancelled.
function LateNote({ run }: { run: RunModel }) {
  const { t } = useTranslation()
  const late = run.steps.find((step) => step.lateStatus !== null)?.lateStatus
  if (late === undefined || late === null) return null
  return (
    <Alert color={tones.info} role="status">
      {t('run.lateStatus')}: {t(`enum.StepStatus.${late}`)}
    </Alert>
  )
}

function OutcomeNotes({ run }: { run: RunModel }) {
  return (
    <>
      <WaitNote run={run} />
      <TimedOutNote run={run} />
      <UnavailableNote run={run} />
      <LateNote run={run} />
    </>
  )
}

// The ways on from a run: restore the snapshot a backup made; try a failed restore again.
function RunActions({ run }: { run: RunModel }) {
  const { t } = useTranslation()
  const { restore } = run
  if (run.kind === 'restore') {
    return restore !== null && run.status === 'failed' ? (
      <ButtonLink
        variant="default"
        to="/sources/$sourceId/snapshots/$snapshotId/restore"
        params={{ sourceId: run.sourceId, snapshotId: restore.snapshotId }}
        search={restoreSearchOf({ agentId: run.agentId, restore })}
      >
        {t('restore.retry')}
      </ButtonLink>
    ) : null
  }
  const backup = run.steps.find((step) => step.backup?.restorable)?.backup
  return (
    <>
      {!run.sourceDeleted && <RunBackupButton sourceId={run.sourceId} />}
      {backup && (
        <RestoreButton sourceId={run.sourceId} snapshotId={backup.snapshotId} restorable />
      )}
    </>
  )
}

// What a restore run restores and where it goes; the step has what it really wrote.
function RestoreFacts({ run }: { run: RunModel }) {
  const { t } = useTranslation()
  const { restore } = run
  if (restore === null) return null
  return (
    <>
      <Table.Tr>
        <Table.Th>{t('restore.snapshot')}</Table.Th>
        <Table.Td>
          <Code>{restore.snapshotId}</Code> <Time value={restore.snapshotCreatedAt} />
        </Table.Td>
      </Table.Tr>
      <Table.Tr>
        <Table.Th>{t('restore.target')}</Table.Th>
        <Table.Td>
          <Mono>{restore.target}</Mono>
        </Table.Td>
      </Table.Tr>
    </>
  )
}

function RunCard({ run }: { run: RunModel }) {
  const { t } = useTranslation()
  const format = useFormat()
  return (
    <Stack>
      <Group>
        <Title order={2}>
          <SourceLabel run={run} />
        </Title>
        <OutcomeBadge run={run} timeoutSeconds={timeoutOfRun(run.steps)} />
        <Badge variant="outline" color={tones.neutral}>
          {t(`enum.RunKind.${run.kind}`)}
        </Badge>
        <RunActions run={run} />
        <CancelRunButton run={run} />
      </Group>
      <OutcomeNotes run={run} />
      <Table>
        <Table.Tbody>
          <Table.Tr>
            <Table.Th>{t('run.trigger')}</Table.Th>
            <Table.Td>
              <TriggerLabel run={run} />
            </Table.Td>
          </Table.Tr>
          {run.cancellation !== null && (
            <Table.Tr>
              <Table.Th>{t('run.cancellation')}</Table.Th>
              <Table.Td>
                {t('run.cancelledBy', {
                  who: run.cancellation.requestedBy,
                  time: format.time(run.cancellation.requestedAt),
                })}
              </Table.Td>
            </Table.Tr>
          )}
          {run.requestedBy !== null && (
            <Table.Tr>
              <Table.Th>{t('restore.requestedBy')}</Table.Th>
              <Table.Td>{run.requestedBy}</Table.Td>
            </Table.Tr>
          )}
          <RestoreFacts run={run} />
          <Table.Tr>
            <Table.Th>{t('runs.queuedAt')}</Table.Th>
            <Table.Td>
              <Time value={run.queuedAt} />
            </Table.Td>
          </Table.Tr>
          <Table.Tr>
            <Table.Th>{t('sources.agent')}</Table.Th>
            <Table.Td>
              <AgentName agentId={run.agentId} />
            </Table.Td>
          </Table.Tr>
        </Table.Tbody>
      </Table>
      {run.message !== null && <Text>{run.message}</Text>}
      {run.steps.map((step) => (
        <StepPanel key={step.id} runId={run.id} step={step} />
      ))}
    </Stack>
  )
}

export function Run() {
  const { t } = useTranslation()
  const { runId } = route.useParams()
  const run = useQuery(runQuery(runId))
  return (
    <Loaded query={run} back={<AppLink to="/runs">{t('runs.title')}</AppLink>}>
      {(data) => <RunCard run={data} />}
    </Loaded>
  )
}
