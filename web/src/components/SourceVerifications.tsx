// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import {
  Alert,
  Badge,
  Button,
  Card,
  Group,
  Modal,
  Select,
  Stack,
  Switch,
  Table,
  Text,
  Title,
} from '@mantine/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { call } from '../api/call'
import { client } from '../api/client'
import {
  PAGE_SIZE,
  sourceQuery,
  sourceVerificationQuery,
  verificationScheduleQuery,
  verifiersQuery,
} from '../api/queries'
import type { components } from '../api/schema'
import { POLL_INTERVAL_MS } from '../polling'
import { tones } from '../theme'
import { useFormat } from '../useFormat'
import { levelWords, restoreTimeText, verifierReasonText } from '../verification'
import { AgentName } from './AgentName'
import { EmptyState } from './EmptyState'
import { ErrorBlock } from './ErrorBlock'
import { AppLink, ButtonLink } from './links'
import { Loaded } from './Loaded'
import { Mono } from './Mono'
import { OutcomeBadge } from './OutcomeBadge'
import { PagedList } from './PagedList'
import { Time } from './Time'
import { usePaged } from './usePaged'

type Hint = components['schemas']['VerificationHintCode']
type Verification = components['schemas']['Verification']

const SHORT_ID = 8

// What the config of the source lacks so that its verification proves more (rule 9): the server decides, the
// console says it and leads to the place where it is put right.
function Hints({ sourceId, hints }: { sourceId: string; hints: Hint[] }) {
  const { t } = useTranslation()
  return (
    <>
      {hints.map((hint) => (
        <Alert key={hint} color={tones.info} role="status">
          {t(`verification.tab.hint.${hint}`)}{' '}
          <AppLink to="/sources/$sourceId/edit" params={{ sourceId }}>
            {t('source.edit')}
          </AppLink>
        </Alert>
      ))}
    </>
  )
}

// The snapshots to pick from: the first page of the source's snapshots; a partial one cannot be verified.
function SnapshotSelect({
  sourceId,
  value,
  onChange,
}: {
  sourceId: string
  value: string
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const format = useFormat()
  const snapshots = useQuery({
    queryKey: ['snapshots', sourceId, 'picker'],
    queryFn: () =>
      call(
        client.GET('/api/v1/sources/{sourceId}/snapshots', {
          params: { path: { sourceId }, query: { limit: PAGE_SIZE } },
        }),
      ),
  })
  const items = snapshots.data?.items ?? []
  return (
    <Select
      label={t('verification.tab.verifyNow.snapshot')}
      allowDeselect={false}
      value={value}
      onChange={(next) => onChange(next ?? 'latest')}
      data={[
        { value: 'latest', label: t('verification.tab.verifyNow.latest') },
        ...items.map((snapshot) => ({
          value: snapshot.id,
          label: `${snapshot.snapshotId} — ${format.time(snapshot.createdAt)}`,
          disabled: snapshot.partial || snapshot.forgottenAt !== null,
        })),
      ]}
    />
  )
}

// «Verify now»: the latest snapshot or one picked from the list; the server refuses a second job and a snapshot
// that cannot be verified, and the refusal is told in words.
function VerifyNow({ sourceId, busy }: { sourceId: string; busy: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [opened, setOpened] = useState(false)
  const [snapshot, setSnapshot] = useState('latest')
  const verify = useMutation({
    mutationFn: () =>
      call(
        client.POST('/api/v1/sources/{sourceId}/verifications', {
          params: { path: { sourceId } },
          body: snapshot === 'latest' ? {} : { snapshotId: snapshot },
        }),
      ),
    onSuccess: async () => {
      setOpened(false)
      await queryClient.invalidateQueries({ queryKey: ['verification', sourceId] })
      // A job that ended before this first look was never seen open: the transition below will not come, so the
      // history and the metric of the source are asked again here.
      if (queryClient.getQueryData(sourceVerificationQuery(sourceId).queryKey)?.openJob === null) {
        void queryClient.invalidateQueries({ queryKey: ['verifications', sourceId] })
        void queryClient.invalidateQueries({ queryKey: sourceQuery(sourceId).queryKey })
      }
    },
  })
  return (
    <>
      <Button disabled={busy} onClick={() => setOpened(true)}>
        {t('verification.tab.verifyNow.button')}
      </Button>
      <Modal
        opened={opened}
        onClose={() => {
          setOpened(false)
          verify.reset()
        }}
        title={t('verification.tab.verifyNow.title')}
      >
        <Stack>
          <SnapshotSelect sourceId={sourceId} value={snapshot} onChange={setSnapshot} />
          {verify.isError && <ErrorBlock error={verify.error} />}
          <Group justify="flex-end">
            <Button loading={verify.isPending} onClick={() => verify.mutate()}>
              {t('verification.tab.verifyNow.confirm')}
            </Button>
          </Group>
        </Stack>
      </Modal>
    </>
  )
}

// The switch of «verify the restore»: the server's state is what it shows; a refusal puts it back and says why.
function EnabledSwitch({ sourceId, enabled }: { sourceId: string; enabled: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const change = useMutation({
    mutationFn: (next: boolean) =>
      call(
        client.PUT('/api/v1/sources/{sourceId}/verification', {
          params: { path: { sourceId } },
          body: { enabled: next },
        }),
      ),
    onSuccess: (state) => {
      queryClient.setQueryData(sourceVerificationQuery(sourceId).queryKey, state)
      void queryClient.invalidateQueries({ queryKey: sourceQuery(sourceId).queryKey })
      void queryClient.invalidateQueries({ queryKey: ['sources'] })
    },
  })
  return (
    <Stack gap="xs">
      <Switch
        label={t('verification.tab.enabled')}
        checked={enabled}
        disabled={change.isPending}
        onChange={(event) => change.mutate(event.currentTarget.checked)}
      />
      {change.isError && <ErrorBlock error={change.error} />}
    </Stack>
  )
}

function OpenJob({ job }: { job: components['schemas']['VerificationJob'] }) {
  const { t } = useTranslation()
  const format = useFormat()
  return (
    <Alert color={tones.info} role="status">
      {t(`verification.tab.job.${job.status}`)}{' '}
      {t('verification.tab.job.deadline', { time: format.time(job.deadline) })}
    </Alert>
  )
}

function Row({ record }: { record: Verification }) {
  const { i18n } = useTranslation()
  return (
    <Table.Tr>
      <Table.Td>
        <AppLink to="/verifications/$verificationId" params={{ verificationId: record.id }}>
          <Time value={record.verifiedAt} />
        </AppLink>
      </Table.Td>
      <Table.Td>
        <Mono>{record.resticSnapshotId.slice(0, SHORT_ID)}</Mono>
      </Table.Td>
      <Table.Td>
        {record.verifierAgentId === null ? '—' : <AgentName agentId={record.verifierAgentId} />}
      </Table.Td>
      <Table.Td>
        <OutcomeBadge status={record.status} />
      </Table.Td>
      <Table.Td>{record.level === 0 ? '—' : levelWords(record.level, i18n.language)}</Table.Td>
      <Table.Td>
        <Mono>{restoreTimeText(record.durationMs, i18n.language)}</Mono>
      </Table.Td>
      <Table.Td>
        <Mono>{restoreTimeText(record.restoreDurationMs, i18n.language)}</Mono>
      </Table.Td>
    </Table.Tr>
  )
}

// While a job is open the history is asked again, and once more when it ends: the new record is in it then, and the
// metric of the source moved.
function History({ sourceId, jobOpen }: { sourceId: string; jobOpen: boolean }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const schedule = useQuery(verificationScheduleQuery())
  const format = useFormat()
  const wasOpen = useRef(jobOpen)
  useEffect(() => {
    if (wasOpen.current && !jobOpen) {
      void queryClient.invalidateQueries({ queryKey: ['verifications', sourceId] })
      void queryClient.invalidateQueries({ queryKey: sourceQuery(sourceId).queryKey })
    }
    wasOpen.current = jobOpen
  }, [jobOpen, queryClient, sourceId])
  const paged = usePaged(
    ['verifications', sourceId],
    (cursor) =>
      call(
        client.GET('/api/v1/sources/{sourceId}/verifications', {
          params: { path: { sourceId }, query: { limit: PAGE_SIZE, cursor: cursor ?? undefined } },
        }),
      ),
    () => (jobOpen ? POLL_INTERVAL_MS : false),
  )
  return (
    <PagedList
      paged={paged}
      empty={
        <EmptyState text={t('verification.tab.empty')}>
          {schedule.data !== undefined && (
            <Text size="sm" c="dimmed">
              {t('verification.tab.nextCycle', { time: format.time(schedule.data.nextRunAt) })}
            </Text>
          )}
        </EmptyState>
      }
    >
      {(records) => (
        <Table>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>{t('verification.tab.columns.time')}</Table.Th>
              <Table.Th>{t('verification.tab.columns.snapshot')}</Table.Th>
              <Table.Th>{t('verification.tab.columns.verifier')}</Table.Th>
              <Table.Th>{t('verification.tab.columns.outcome')}</Table.Th>
              <Table.Th>{t('verification.tab.columns.level')}</Table.Th>
              <Table.Th>{t('verification.tab.columns.duration')}</Table.Th>
              <Table.Th>{t('verification.tab.columns.restoreTime')}</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {records.map((record) => (
              <Row key={record.id} record={record} />
            ))}
          </Table.Tbody>
        </Table>
      )}
    </PagedList>
  )
}

// Which verifiers can check the source and, for each that cannot, every reason (rule 12).
function Fitting({ sourceId }: { sourceId: string }) {
  const { t, i18n } = useTranslation()
  const verifiers = useQuery(verifiersQuery(sourceId))
  return (
    <Card withBorder>
      <Title order={5}>{t('verification.tab.fitting')}</Title>
      <Loaded query={verifiers}>
        {(list) =>
          list.length === 0 ? (
            <Text c="dimmed">{t('verification.tab.noVerifiers')}</Text>
          ) : (
            <Stack gap="xs">
              {list.map((verifier) => (
                <Group key={verifier.agentId} gap="xs">
                  <AppLink to="/agents/$agentId" params={{ agentId: verifier.agentId }}>
                    <Mono>{verifier.hostname}</Mono>
                  </AppLink>
                  {(verifier.reasons ?? []).length === 0 ? (
                    <Badge color={tones.success}>{t('verification.tab.fits')}</Badge>
                  ) : (
                    (verifier.reasons ?? []).map((reason) => (
                      <Badge key={reason} color={tones.warning}>
                        {verifierReasonText(reason, i18n.language)}
                      </Badge>
                    ))
                  )}
                </Group>
              ))}
            </Stack>
          )
        }
      </Loaded>
    </Card>
  )
}

// The tab «Verifications» of a source: the switch, «verify now», what to set up, who can check it and the history.
export function SourceVerifications({ sourceId }: { sourceId: string }) {
  const { t } = useTranslation()
  const state = useQuery(sourceVerificationQuery(sourceId))
  return (
    <Stack>
      <Loaded query={state}>
        {(data) => (
          <>
            <Group>
              <EnabledSwitch sourceId={sourceId} enabled={data.enabled} />
              <VerifyNow sourceId={sourceId} busy={data.openJob !== null} />
              <ButtonLink
                to="/sources/$sourceId/verification-report"
                params={{ sourceId }}
                variant="default"
              >
                {t('verification.tab.report')}
              </ButtonLink>
            </Group>
            {data.openJob !== null && <OpenJob job={data.openJob} />}
            <Hints sourceId={sourceId} hints={data.hints} />
          </>
        )}
      </Loaded>
      <Fitting sourceId={sourceId} />
      <History sourceId={sourceId} jobOpen={state.data?.openJob != null} />
    </Stack>
  )
}
