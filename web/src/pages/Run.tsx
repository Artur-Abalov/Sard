// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Badge, Code, Group, Stack, Table, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { agentQuery, runQuery, type Run as RunModel } from '../api/queries'
import { AgentName } from '../components/AgentName'
import { AppLink, ButtonLink } from '../components/links'
import { Loaded } from '../components/Loaded'
import { RestoreButton } from '../components/RestoreButton'
import { RunBackupButton } from '../components/RunBackupButton'
import { SourceLabel, TriggerLabel } from '../components/RunsTable'
import { StatusBadge } from '../components/StatusBadge'
import { StepPanel } from '../components/StepPanel'
import { restoreSearchOf } from '../restoreView'
import { waitsForAgent } from '../stepNotes'
import { tones } from '../theme'
import { Mono } from '../components/Mono'
import { Time } from '../components/Time'

const route = getRouteApi('/_app/runs/$runId')

// A queued run of an agent that is offline starts when the agent connects; both facts are the server's.
function WaitingForAgent({ run }: { run: RunModel }) {
  const { t } = useTranslation()
  const agent = useQuery(agentQuery(run.agentId))
  if (!waitsForAgent(run.status, agent.data?.status)) return null
  return (
    <Alert color={tones.info} role="status">
      {t('run.waitingForAgent')} <AgentName agentId={run.agentId} />
    </Alert>
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
  return (
    <Stack>
      <Group>
        <Title order={2}>
          <SourceLabel run={run} />
        </Title>
        <StatusBadge group="RunStatus" value={run.status} />
        <Badge variant="outline" color={tones.neutral}>
          {t(`enum.RunKind.${run.kind}`)}
        </Badge>
        <RunActions run={run} />
      </Group>
      <WaitingForAgent run={run} />
      <Table>
        <Table.Tbody>
          <Table.Tr>
            <Table.Th>{t('run.trigger')}</Table.Th>
            <Table.Td>
              <TriggerLabel run={run} />
            </Table.Td>
          </Table.Tr>
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
