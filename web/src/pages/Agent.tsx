// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import {
  Alert,
  Badge,
  Button,
  Card,
  Group,
  List,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { call } from '../api/call'
import { client } from '../api/client'
import { agentQuery, PAGE_SIZE, type AgentDetails } from '../api/queries'
import { AgentMarks } from '../components/AgentMarks'
import { AgentUpgradeBlock } from '../components/AgentUpgradeBlock'
import { ConfirmModal } from '../components/ConfirmModal'
import { EmptyState } from '../components/EmptyState'
import { ErrorBlock } from '../components/ErrorBlock'
import { AppLink, ButtonLink } from '../components/links'
import { Loaded } from '../components/Loaded'
import { PagedList } from '../components/PagedList'
import { RepoInitHint } from '../components/RepoInitHint'
import { StatusBadge } from '../components/StatusBadge'
import { usePaged } from '../components/usePaged'
import { EMPTY } from '../format'
import { canConfirmRevoke, revokeQuery, SELF_AGENT_CONFIRMATION } from '../selfAgent'
import { tones } from '../theme'
import { Mono } from '../components/Mono'
import { Time } from '../components/Time'

const route = getRouteApi('/_app/agents/$agentId')

function Repositories({ agent }: { agent: AgentDetails }) {
  const { t } = useTranslation()
  return (
    <Card withBorder>
      <Title order={4}>{t('agent.repositories')}</Title>
      <Stack mt="xs">
        {agent.repositories.map((repository) => (
          <Stack key={repository.name} gap="xs">
            <Group>
              <Mono fw={500}>{repository.name}</Mono>
              <Text c="dimmed">{repository.backend}</Text>
              {repository.repositoryId === null ? (
                <Badge color={tones.warning}>{t('agent.notInitialized')}</Badge>
              ) : (
                <Badge color={tones.success}>
                  {t('agent.initialized', { id: repository.repositoryId })}
                </Badge>
              )}
            </Group>
            {repository.repositoryId === null && <RepoInitHint repository={repository.name} />}
          </Stack>
        ))}
      </Stack>
    </Card>
  )
}

function Names({ title, names }: { title: string; names: string[] }) {
  const { t } = useTranslation()
  return (
    <Card withBorder>
      <Title order={4}>{title}</Title>
      {names.length === 0 ? (
        <Text c="dimmed">{t('agent.none')}</Text>
      ) : (
        <List>
          {names.map((name) => (
            <List.Item key={name}>
              <Mono>{name}</Mono>
            </List.Item>
          ))}
        </List>
      )}
    </Card>
  )
}

function Plugins({ agent }: { agent: AgentDetails }) {
  const { t } = useTranslation()
  return (
    <Card withBorder>
      <Title order={4}>{t('agent.plugins')}</Title>
      <Table>
        <Table.Tbody>
          {agent.plugins.map((plugin) => (
            <Table.Tr key={plugin.name}>
              <Table.Td>
                <Mono>{plugin.name}</Mono>
              </Table.Td>
              <Table.Td>
                <Mono>{plugin.version}</Mono>
              </Table.Td>
              <Table.Td>
                {plugin.actions.map((action) => t(`enum.StepAction.${action}`)).join(', ')}
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    </Card>
  )
}

// What the agent announced in its last Register; nothing yet when it never registered.
function Snapshot({ agent }: { agent: AgentDetails }) {
  const { t } = useTranslation()
  if (agent.protocolVersion === null) {
    return <Alert color={tones.info}>{t('agent.neverRegistered')}</Alert>
  }
  return (
    <>
      <Plugins agent={agent} />
      <Repositories agent={agent} />
      <Names title={t('agent.secrets')} names={agent.secretNames} />
      <Names title={t('agent.scripts')} names={agent.scriptNames} />
    </>
  )
}

function DuplicateNote() {
  const { t } = useTranslation()
  return (
    <Alert color={tones.notice} title={t('agent.duplicateTitle')}>
      <Text size="sm">{t('agent.duplicateWhy')}</Text>
      <AppLink to="/tokens" search={{ create: true }}>
        {t('dashboard.issueToken')}
      </AppLink>
    </Alert>
  )
}

function AgentSources({ agentId }: { agentId: string }) {
  const { t } = useTranslation()
  const paged = usePaged(['sources', 'of-agent', agentId], (cursor) =>
    call(
      client.GET('/api/v1/sources', {
        params: { query: { agentId, limit: PAGE_SIZE, cursor: cursor ?? undefined } },
      }),
    ),
  )
  return (
    <Card withBorder>
      <Group justify="space-between">
        <Title order={4}>{t('agent.sources')}</Title>
        <ButtonLink to="/sources/new" search={{ agentId }} size="xs">
          {t('sources.create')}
        </ButtonLink>
      </Group>
      <PagedList paged={paged} empty={<EmptyState text={t('agent.noSources')} />}>
        {(sources) => (
          <List>
            {sources.map((source) => (
              <List.Item key={source.id}>
                <AppLink to="/sources/$sourceId" params={{ sourceId: source.id }}>
                  {source.name}
                </AppLink>
              </List.Item>
            ))}
          </List>
        )}
      </PagedList>
    </Card>
  )
}

function RevokeButton({ agent }: { agent: AgentDetails }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [opened, { open, close }] = useDisclosure()
  const [typed, setTyped] = useState('')
  const closeAndForget = () => {
    setTyped('')
    close()
  }
  const revoke = useMutation({
    mutationFn: () =>
      call(
        client.POST('/api/v1/agents/{agentId}/revoke', {
          params: { path: { agentId: agent.id }, query: revokeQuery(agent.builtin, typed) },
        }),
      ),
    onSuccess: (revoked) => {
      queryClient.setQueryData(agentQuery(agent.id).queryKey, revoked)
      void queryClient.invalidateQueries({ queryKey: ['agents'] })
      closeAndForget()
    },
  })
  return (
    <>
      <Button variant="default" c={tones.error} onClick={open}>
        {t('agent.revoke')}
      </Button>
      <ConfirmModal
        opened={opened}
        title={t('agent.revokeTitle', { host: agent.hostname })}
        confirmLabel={t('agent.revoke')}
        busy={revoke.isPending}
        disabled={!canConfirmRevoke(agent.builtin, typed)}
        onConfirm={() => revoke.mutate()}
        onClose={closeAndForget}
      >
        <List>
          <List.Item>{t('agent.revokeConnection')}</List.Item>
          <List.Item>{t('agent.revokeRuns')}</List.Item>
          <List.Item>{t('agent.revokeHistory')}</List.Item>
        </List>
        {agent.builtin && (
          <TextInput
            label={t('agent.revokeConfirmLabel')}
            placeholder={SELF_AGENT_CONFIRMATION}
            value={typed}
            onChange={(event) => setTyped(event.currentTarget.value)}
            data-autofocus
          />
        )}
        {revoke.isError && <ErrorBlock error={revoke.error} />}
      </ConfirmModal>
    </>
  )
}

function Facts({ agent }: { agent: AgentDetails }) {
  const { t } = useTranslation()
  return (
    <Table>
      <Table.Tbody>
        <Table.Tr>
          <Table.Th>{t('agents.version')}</Table.Th>
          <Table.Td>
            <Mono>{agent.agentVersion ?? EMPTY}</Mono>
          </Table.Td>
        </Table.Tr>
        <Table.Tr>
          <Table.Th>{t('agents.os')}</Table.Th>
          <Table.Td>{agent.os === null ? EMPTY : `${agent.os} ${agent.arch ?? ''}`}</Table.Td>
        </Table.Tr>
        <Table.Tr>
          <Table.Th>{t('agents.lastSeen')}</Table.Th>
          <Table.Td>
            {agent.lastSeenAt === null ? (
              t('agents.neverConnected')
            ) : (
              <Time value={agent.lastSeenAt} />
            )}
          </Table.Td>
        </Table.Tr>
        <Table.Tr>
          <Table.Th>{t('agent.registeredAt')}</Table.Th>
          <Table.Td>
            <Time value={agent.registeredAt} />
          </Table.Td>
        </Table.Tr>
      </Table.Tbody>
    </Table>
  )
}

function AgentCard({ agent }: { agent: AgentDetails }) {
  return (
    <Stack>
      <Group>
        <Title order={2}>{agent.hostname}</Title>
        <StatusBadge group="AgentStatus" value={agent.status} />
        <AgentMarks agent={agent} />
        {agent.revokedAt === null && <RevokeButton agent={agent} />}
      </Group>
      {agent.duplicateSessionAt !== null && <DuplicateNote />}
      {agent.outdated && <AgentUpgradeBlock agent={agent} />}
      <Facts agent={agent} />
      <Snapshot agent={agent} />
      <AgentSources agentId={agent.id} />
    </Stack>
  )
}

export function Agent() {
  const { t } = useTranslation()
  const { agentId } = route.useParams()
  const agent = useQuery(agentQuery(agentId))
  return (
    <Loaded query={agent} back={<AppLink to="/agents">{t('agents.title')}</AppLink>}>
      {(data) => <AgentCard agent={data} />}
    </Loaded>
  )
}
