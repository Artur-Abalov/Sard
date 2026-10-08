// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import {
  Alert,
  Button,
  Group,
  Modal,
  NumberInput,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, call } from '../api/call'
import { client } from '../api/client'
import { fieldFailures } from '../errors'
import { mapField } from '../fieldPath'
import { caQuery, PAGE_SIZE, tokenQuery, type EnrollmentToken } from '../api/queries'
import type { components } from '../api/schema'
import { AgentInstallBlock } from '../components/AgentInstallBlock'
import { AgentName } from '../components/AgentName'
import { ConfirmModal } from '../components/ConfirmModal'
import { CopyBox } from '../components/CopyBox'
import { EmptyState } from '../components/EmptyState'
import { ErrorBlock } from '../components/ErrorBlock'
import { AppLink } from '../components/links'
import { PagedList } from '../components/PagedList'
import { StatusBadge } from '../components/StatusBadge'
import { usePaged } from '../components/usePaged'
import { EMPTY } from '../format'
import { canRevokeToken, ttlSeconds, type TtlUnit } from '../tokens'
import { useFormat } from '../useFormat'
import { tones } from '../theme'
import { Time } from '../components/Time'

const route = getRouteApi('/_app/tokens')

type Created = components['schemas']['CreatedEnrollmentToken']

// What the poll of the new token has found out about it.
function TokenOutcome({ token }: { token: EnrollmentToken | undefined }) {
  const { t } = useTranslation()
  switch (token?.status) {
    case 'used':
      return (
        <Alert color={tones.success}>
          {t('tokens.agentConnected')}{' '}
          {token.agentId !== null && <AgentName agentId={token.agentId} />}
        </Alert>
      )
    case 'expired':
      return <Alert color={tones.notice}>{t('tokens.expiredNow')}</Alert>
    case 'revoked':
      return <Alert color={tones.notice}>{t('tokens.revokedNow')}</Alert>
    default:
      return <Text>{t('tokens.waiting')}</Text>
  }
}

// Where the new token stands while its window is open: waiting, used by an agent, or no longer usable.
function TokenProgress({ tokenId }: { tokenId: string }) {
  const token = useQuery(tokenQuery(tokenId, true))
  return (
    <Stack gap="xs" role="status">
      <TokenOutcome token={token.data} />
    </Stack>
  )
}

// The token string and its command, shown once; the server keeps neither.
function CreatedToken({ created }: { created: Created }) {
  const { t } = useTranslation()
  const format = useFormat()
  return (
    <Stack>
      <Alert color={tones.warning}>{t('tokens.shownOnce')}</Alert>
      <CopyBox value={created.token} label={t('tokens.token')} />
      <CopyBox value={created.enrollCommand} label={t('tokens.command')} />
      {!created.agentEndpointConfigured && (
        <Alert color={tones.notice}>{t('tokens.endpointDerived')}</Alert>
      )}
      <Text size="sm">{t('tokens.expiresAt', { time: format.time(created.expiresAt) })}</Text>
      <TokenProgress tokenId={created.id} />
      <AgentInstallBlock open enrollCommand={created.enrollCommand} />
    </Stack>
  )
}

function CreateForm({ onCreated }: { onCreated: (created: Created) => void }) {
  const { t } = useTranslation()
  const [label, setLabel] = useState('')
  const [ttl, setTtl] = useState<number | string>('')
  const [unit, setUnit] = useState<TtlUnit>('hours')
  const create = useMutation({
    mutationFn: () =>
      call(
        client.POST('/api/v1/enrollment-tokens', {
          body: { ttlSeconds: ttlSeconds(ttl, unit), label: label === '' ? undefined : label },
        }),
      ),
    onSuccess: onCreated,
  })
  const fields = create.error instanceof ApiError ? create.error.body : undefined
  return (
    <Stack>
      <TextInput
        label={t('tokens.label')}
        value={label}
        onChange={(event) => setLabel(event.currentTarget.value)}
        error={refused(fields, 'label') ? t('errors.validation_failed') : undefined}
      />
      <Group align="flex-end">
        <NumberInput
          label={t('tokens.ttl')}
          description={t('tokens.ttlHint')}
          value={ttl}
          onChange={setTtl}
          min={0}
          error={refused(fields, 'ttl') ? t('errors.validation_failed') : undefined}
        />
        <Select
          aria-label={t('tokens.ttlUnit')}
          allowDeselect={false}
          value={unit}
          onChange={(value) => setUnit((value as TtlUnit | null) ?? 'hours')}
          data={(['minutes', 'hours', 'days'] as const).map((value) => ({
            value,
            label: t(`tokens.units.${value}`),
          }))}
        />
      </Group>
      {create.isError && <ErrorBlock error={create.error} />}
      <Button loading={create.isPending} onClick={() => create.mutate()}>
        {t('tokens.create')}
      </Button>
    </Stack>
  )
}

// Whether the server refused this field of the form (a 422 names it by its path).
function refused(body: unknown, kind: 'ttl' | 'label'): boolean {
  return fieldFailures(body).some((failure) => mapField(failure.field, []).kind === kind)
}

function CreateTokenModal({ opened, onClose }: { opened: boolean; onClose: () => void }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [created, setCreated] = useState<Created | null>(null)
  function close() {
    setCreated(null)
    onClose()
  }
  return (
    <Modal opened={opened} onClose={close} title={t('tokens.createTitle')} size="lg">
      {created === null ? (
        <CreateForm
          onCreated={(token) => {
            setCreated(token)
            void queryClient.invalidateQueries({ queryKey: ['tokens'] })
          }}
        />
      ) : (
        <CreatedToken created={created} />
      )}
    </Modal>
  )
}

// A token that lost the race to an agent or to its own expiry says so, with the agent when it has one.
function RevokeRefused({ error }: { error: ApiError }) {
  const { t } = useTranslation()
  const body = error.body as { agentId?: string | null } | undefined
  return (
    <Alert color={tones.notice}>
      {error.failure.kind === 'problem' && error.failure.code === 'token_used'
        ? t('tokens.alreadyUsed')
        : t('tokens.alreadyExpired')}{' '}
      {body?.agentId != null && <AgentName agentId={body.agentId} />}
    </Alert>
  )
}

function RevokeButton({ token }: { token: EnrollmentToken }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [opened, { open, close }] = useDisclosure()
  const revoke = useMutation({
    mutationFn: () =>
      call(
        client.POST('/api/v1/enrollment-tokens/{tokenId}/revoke', {
          params: { path: { tokenId: token.id } },
        }),
      ),
    onSettled: () => queryClient.invalidateQueries({ queryKey: ['tokens'] }),
    onSuccess: close,
  })
  return (
    <>
      <Button variant="default" c={tones.error} size="xs" onClick={open}>
        {t('tokens.revoke')}
      </Button>
      <ConfirmModal
        opened={opened}
        title={t('tokens.revokeTitle')}
        confirmLabel={t('tokens.revoke')}
        busy={revoke.isPending}
        onConfirm={() => revoke.mutate()}
        onClose={close}
      >
        <Text>{t('tokens.revokeText')}</Text>
        {revoke.error instanceof ApiError && revoke.error.failure.kind === 'problem' && (
          <RevokeRefused error={revoke.error} />
        )}
        {revoke.isError &&
          !(revoke.error instanceof ApiError && revoke.error.body !== undefined) && (
            <ErrorBlock error={revoke.error} />
          )}
      </ConfirmModal>
    </>
  )
}

function TokenRow({ token }: { token: EnrollmentToken }) {
  return (
    <Table.Tr>
      <Table.Td>
        <StatusBadge group="EnrollmentTokenStatus" value={token.status} />
      </Table.Td>
      <Table.Td>{token.label ?? EMPTY}</Table.Td>
      <Table.Td>
        <Time value={token.createdAt} />
      </Table.Td>
      <Table.Td>
        <Time value={token.expiresAt} />
      </Table.Td>
      <Table.Td>{token.agentId === null ? EMPTY : <AgentName agentId={token.agentId} />}</Table.Td>
      <Table.Td>{canRevokeToken(token.status) && <RevokeButton token={token} />}</Table.Td>
    </Table.Tr>
  )
}

// The CA fingerprint the server sends as is: it ends every token and opens the server's start log.
// A failed load shows its own message and leaves the token list alone.
function CaFingerprint() {
  const { t } = useTranslation()
  const ca = useQuery(caQuery())
  if (ca.isError) return <ErrorBlock error={ca.error} onRetry={() => void ca.refetch()} />
  if (ca.data === undefined) return null
  return (
    <Stack gap={4}>
      <CopyBox value={ca.data.fingerprint} label={t('tokens.caFingerprint')} unbroken />
      <Text size="xs" c="dimmed">
        {t('tokens.caFingerprintHint')}
      </Text>
    </Stack>
  )
}

export function Tokens() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { create, hint } = route.useSearch()
  const [opened, setOpened] = useState(create === true)
  const paged = usePaged(['tokens', 'list'], (cursor) =>
    call(
      client.GET('/api/v1/enrollment-tokens', {
        params: { query: { limit: PAGE_SIZE, cursor: cursor ?? undefined } },
      }),
    ),
  )
  return (
    <Stack>
      <Group justify="space-between">
        <Title order={2}>{t('tokens.title')}</Title>
        <Button onClick={() => setOpened(true)}>{t('tokens.create')}</Button>
      </Group>
      <CaFingerprint />
      {hint === 'enroll' && <Alert color={tones.info}>{t('tokens.enrollHint')}</Alert>}
      <CreateTokenModal
        opened={opened}
        onClose={() => {
          setOpened(false)
          if (create === true) void navigate({ to: '/tokens', search: {}, replace: true })
        }}
      />
      <PagedList
        paged={paged}
        empty={
          <EmptyState text={t('tokens.empty')}>
            <AppLink to="/tokens" search={{ create: true }}>
              {t('tokens.create')}
            </AppLink>
          </EmptyState>
        }
      >
        {(tokens) => (
          <Table>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>{t('tokens.status')}</Table.Th>
                <Table.Th>{t('tokens.label')}</Table.Th>
                <Table.Th>{t('tokens.createdAt')}</Table.Th>
                <Table.Th>{t('tokens.expires')}</Table.Th>
                <Table.Th>{t('tokens.agent')}</Table.Th>
                <Table.Th />
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {tokens.map((token) => (
                <TokenRow key={token.id} token={token} />
              ))}
            </Table.Tbody>
          </Table>
        )}
      </PagedList>
    </Stack>
  )
}
