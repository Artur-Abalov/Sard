// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Group, Stack, Table, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { agentQuery, runQuery, type Run as RunModel } from '../api/queries'
import { AgentName } from '../components/AgentName'
import { AppLink } from '../components/links'
import { Loaded } from '../components/Loaded'
import { RunBackupButton } from '../components/RunBackupButton'
import { SourceLabel } from '../components/RunsTable'
import { StatusBadge } from '../components/StatusBadge'
import { StepPanel } from '../components/StepPanel'
import { waitsForAgent } from '../stepNotes'
import { useFormat } from '../useFormat'
import { tones } from '../theme'

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

function RunCard({ run }: { run: RunModel }) {
  const { t } = useTranslation()
  const format = useFormat()
  return (
    <Stack>
      <Group>
        <Title order={2}>
          <SourceLabel run={run} />
        </Title>
        <StatusBadge group="RunStatus" value={run.status} />
        {!run.sourceDeleted && <RunBackupButton sourceId={run.sourceId} />}
      </Group>
      <WaitingForAgent run={run} />
      <Table>
        <Table.Tbody>
          <Table.Tr>
            <Table.Th>{t('run.trigger')}</Table.Th>
            <Table.Td>{t(`enum.RunTrigger.${run.trigger}`)}</Table.Td>
          </Table.Tr>
          <Table.Tr>
            <Table.Th>{t('runs.queuedAt')}</Table.Th>
            <Table.Td>{format.time(run.queuedAt)}</Table.Td>
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
