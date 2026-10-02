// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Badge, Card, Code, Group, Stack, Table, Text, Title } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { Button } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { ApiError, call } from '../api/call'
import { client } from '../api/client'
import { PAGE_SIZE, sourceQuery, type Source as SourceModel } from '../api/queries'
import type { components } from '../api/schema'
import { AgentName } from '../components/AgentName'
import { ConfirmModal } from '../components/ConfirmModal'
import { EmptyState } from '../components/EmptyState'
import { ErrorBlock } from '../components/ErrorBlock'
import { AppLink, ButtonLink } from '../components/links'
import { Loaded } from '../components/Loaded'
import { PagedList } from '../components/PagedList'
import { RunBackupButton } from '../components/RunBackupButton'
import { usePaged } from '../components/usePaged'
import { useFormat } from '../useFormat'

const route = getRouteApi('/_app/sources/$sourceId/')

type Snapshot = components['schemas']['Snapshot']

function SnapshotRow({ snapshot }: { snapshot: Snapshot }) {
  const { t } = useTranslation()
  const format = useFormat()
  return (
    <Table.Tr>
      <Table.Td>
        <Code>{snapshot.snapshotId}</Code>
        {snapshot.partial && (
          <>
            {' '}
            <Badge color="yellow" title={t('source.partialWhy')}>
              {t('source.partial')}
            </Badge>
          </>
        )}
      </Table.Td>
      <Table.Td>{snapshot.repositoryName}</Table.Td>
      <Table.Td>{format.bytes(snapshot.totalBytes)}</Table.Td>
      <Table.Td>{format.bytes(snapshot.addedBytes)}</Table.Td>
      <Table.Td>{format.time(snapshot.createdAt)}</Table.Td>
      <Table.Td>
        <AppLink to="/runs/$runId" params={{ runId: snapshot.runId }}>
          {t('runs.open')}
        </AppLink>
      </Table.Td>
    </Table.Tr>
  )
}

// The snapshots of the source; a failure here leaves the rest of the card as it is.
function Snapshots({ sourceId }: { sourceId: string }) {
  const { t } = useTranslation()
  const paged = usePaged(['snapshots', sourceId], (cursor) =>
    call(
      client.GET('/api/v1/sources/{sourceId}/snapshots', {
        params: {
          path: { sourceId },
          query: { limit: PAGE_SIZE, cursor: cursor ?? undefined },
        },
      }),
    ),
  )
  return (
    <Card withBorder>
      <Title order={4}>{t('source.snapshots')}</Title>
      <PagedList
        paged={paged}
        empty={
          <EmptyState text={t('source.noSnapshots')}>
            <RunBackupButton sourceId={sourceId} />
          </EmptyState>
        }
      >
        {(snapshots) => (
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t('source.snapshot')}</Table.Th>
                <Table.Th>{t('sources.repository')}</Table.Th>
                <Table.Th>{t('source.total')}</Table.Th>
                <Table.Th>{t('source.added')}</Table.Th>
                <Table.Th>{t('source.createdAt')}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {snapshots.map((snapshot) => (
                <SnapshotRow key={snapshot.id} snapshot={snapshot} />
              ))}
            </Table.Tbody>
          </Table>
        )}
      </PagedList>
    </Card>
  )
}

function DeleteRefused({ error }: { error: unknown }) {
  const { t } = useTranslation()
  const body = error instanceof ApiError ? (error.body as { activeRunId?: string }) : undefined
  const active =
    error instanceof ApiError &&
    error.failure.kind === 'problem' &&
    error.failure.code === 'run_active'
  if (!active) return <ErrorBlock error={error} />
  return (
    <Alert color="blue" role="status">
      {t('source.deleteActive')}{' '}
      {body?.activeRunId && (
        <AppLink to="/runs/$runId" params={{ runId: body.activeRunId }}>
          {t('sources.openRun')}
        </AppLink>
      )}
    </Alert>
  )
}

function DeleteButton({ source }: { source: SourceModel }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [opened, { open, close }] = useDisclosure()
  const remove = useMutation({
    mutationFn: () =>
      call(
        client.DELETE('/api/v1/sources/{sourceId}', { params: { path: { sourceId: source.id } } }),
      ),
    onSuccess: async () => {
      queryClient.removeQueries({ queryKey: sourceQuery(source.id).queryKey })
      void queryClient.invalidateQueries({ queryKey: ['sources'] })
      await navigate({ to: '/sources' })
    },
  })
  return (
    <>
      <Button color="red" variant="light" onClick={open}>
        {t('source.delete')}
      </Button>
      <ConfirmModal
        opened={opened}
        title={t('source.deleteTitle', { name: source.name })}
        confirmLabel={t('source.delete')}
        busy={remove.isPending}
        onConfirm={() => remove.mutate()}
        onClose={close}
      >
        <Text>{t('source.deleteText')}</Text>
        {remove.isError && <DeleteRefused error={remove.error} />}
      </ConfirmModal>
    </>
  )
}

function SourceCard({ source }: { source: SourceModel }) {
  const { t } = useTranslation()
  const format = useFormat()
  return (
    <Stack>
      <Group>
        <Title order={2}>{source.name}</Title>
        <RunBackupButton sourceId={source.id} />
        <ButtonLink to="/sources/$sourceId/edit" params={{ sourceId: source.id }} variant="default">
          {t('source.edit')}
        </ButtonLink>
        <DeleteButton source={source} />
      </Group>
      <Table>
        <Table.Tbody>
          <Table.Tr>
            <Table.Th>{t('sources.agent')}</Table.Th>
            <Table.Td>
              <AgentName agentId={source.agentId} />
            </Table.Td>
          </Table.Tr>
          <Table.Tr>
            <Table.Th>{t('sources.plugin')}</Table.Th>
            <Table.Td>{source.plugin}</Table.Td>
          </Table.Tr>
          <Table.Tr>
            <Table.Th>{t('sources.repository')}</Table.Th>
            <Table.Td>{source.repositoryName}</Table.Td>
          </Table.Tr>
          <Table.Tr>
            <Table.Th>{t('source.updatedAt')}</Table.Th>
            <Table.Td>{format.time(source.updatedAt)}</Table.Td>
          </Table.Tr>
        </Table.Tbody>
      </Table>
      <Card withBorder>
        <Title order={4}>{t('source.config')}</Title>
        <Code block>{JSON.stringify(source.config, null, 2)}</Code>
      </Card>
      <Snapshots sourceId={source.id} />
    </Stack>
  )
}

export function Source() {
  const { t } = useTranslation()
  const { sourceId } = route.useParams()
  const source = useQuery(sourceQuery(sourceId))
  return (
    <Loaded query={source} back={<AppLink to="/sources">{t('sources.title')}</AppLink>}>
      {(data) => <SourceCard source={data} />}
    </Loaded>
  )
}
