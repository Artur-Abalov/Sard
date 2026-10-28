// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Badge, Card, Code, Group, Stack, Table, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { getRouteApi } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { isNotFound } from '../api/call'
import { sourceQuery, verificationQuery } from '../api/queries'
import type { components } from '../api/schema'
import { AgentName } from '../components/AgentName'
import { OutcomeBadge } from '../components/OutcomeBadge'
import { AppLink } from '../components/links'
import { Loaded } from '../components/Loaded'
import { Mono } from '../components/Mono'
import { Time } from '../components/Time'
import { tones } from '../theme'
import { useFormat } from '../useFormat'
import {
  levelWords,
  mismatchText,
  restoreTimeText,
  stageHint,
  stageLabel,
  verifierReasonText,
} from '../verification'

const route = getRouteApi('/_app/verifications/$verificationId')

type Verification = components['schemas']['Verification']
type Stage = components['schemas']['VerificationStage']

const SHORT_ID = 8

// A stage of the pipeline: the label, its status and how long it took; the evidence opens beneath it (rule 10).
function StageRow({ stage }: { stage: Stage }) {
  const { t, i18n } = useTranslation()
  const format = useFormat()
  const hint = stage.status === 'cannot_run' ? stageHint(stage.name, i18n.language) : null
  return (
    <Table.Tr>
      <Table.Td>{stageLabel(stage.name, i18n.language)}</Table.Td>
      <Table.Td>{stage.status === null ? '—' : <StageStatus status={stage.status} />}</Table.Td>
      <Table.Td>
        <Mono>{format.duration(stage.startedAt, stage.finishedAt)}</Mono>
      </Table.Td>
      <Table.Td>
        <Stack gap={4}>
          {hint !== null && (
            <Alert color={tones.warning} role="status">
              {hint}
            </Alert>
          )}
          <Text size="sm">
            {t('verification.record.exitCode')}: <Mono>{stage.exitCode}</Mono>
            {stage.imageDigest !== '' && (
              <>
                {' · '}
                {t('verification.record.imageDigest')}: <Mono>{stage.imageDigest}</Mono>
              </>
            )}
          </Text>
          {stage.output !== '' && <Code block>{stage.output}</Code>}
        </Stack>
      </Table.Td>
    </Table.Tr>
  )
}

function StageStatus({ status }: { status: NonNullable<Stage['status']> }) {
  const { t } = useTranslation()
  const tone = {
    passed: tones.success,
    failed: tones.error,
    cannot_run: tones.warning,
    not_applicable: tones.neutral,
  }[status]
  return <Badge color={tone}>{t(`verification.record.stageStatus.${status}`)}</Badge>
}

function Stages({ stages }: { stages: Stage[] }) {
  const { t } = useTranslation()
  if (stages.length === 0) return <Text c="dimmed">{t('verification.record.noStages')}</Text>
  return (
    <Table>
      <Table.Thead>
        <Table.Tr>
          <Table.Th>{t('verification.record.stage')}</Table.Th>
          <Table.Th>{t('verification.record.status')}</Table.Th>
          <Table.Th>{t('verification.tab.columns.duration')}</Table.Th>
          <Table.Th>{t('verification.record.evidence')}</Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {stages.map((stage) => (
          <StageRow key={stage.name} stage={stage} />
        ))}
      </Table.Tbody>
    </Table>
  )
}

// The mismatches in words: what the backup lacks that the source has.
function Mismatches({ record }: { record: Verification }) {
  const { t, i18n } = useTranslation()
  if (record.mismatches.length === 0) return null
  return (
    <Stack gap="xs">
      <Title order={5}>{t('verification.record.mismatches')}</Title>
      {record.mismatches.map((mismatch) => (
        <Text key={mismatch.name}>{mismatchText(mismatch, i18n.language)}</Text>
      ))}
    </Stack>
  )
}

// The source's fingerprint beside the copy's, field by field, with the mark of the rule and of the outcome.
function Fingerprint({ record }: { record: Verification }) {
  const { t } = useTranslation()
  if (record.fingerprint.length === 0) return null
  return (
    <Card withBorder>
      <Title order={5}>{t('verification.record.fingerprint')}</Title>
      <Table>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>{t('verification.record.field')}</Table.Th>
            <Table.Th>{t('verification.record.rule')}</Table.Th>
            <Table.Th>{t('verification.record.source')}</Table.Th>
            <Table.Th>{t('verification.record.copy')}</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {record.fingerprint.map((row) => (
            <Table.Tr key={row.name}>
              <Table.Td>
                <Mono>{row.name}</Mono>
              </Table.Td>
              <Table.Td>{t(`verification.record.rules.${row.rule}`)}</Table.Td>
              <Table.Td>
                <Mono>{row.source}</Mono>
              </Table.Td>
              <Table.Td>
                <Group gap="xs">
                  {row.copy !== null && <Mono>{row.copy}</Mono>}
                  <Badge
                    color={
                      row.outcome === 'mismatched'
                        ? tones.error
                        : row.outcome === 'matched'
                          ? tones.success
                          : tones.neutral
                    }
                  >
                    {t(`verification.record.outcomes.${row.outcome}`)}
                  </Badge>
                </Group>
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
      {record.hiddenFacts > 0 && (
        <Text size="sm" c="dimmed">
          {t('verification.record.hiddenFacts', { count: record.hiddenFacts })}
        </Text>
      )}
    </Card>
  )
}

// Why no verifier took the job: every one that did not fit, with every reason (rule 5).
function Unsuitable({ record }: { record: Verification }) {
  const { t, i18n } = useTranslation()
  if (record.unsuitable === null || record.unsuitable.length === 0) return null
  return (
    <Stack gap="xs">
      <Title order={5}>{t('verification.record.unsuitable')}</Title>
      {record.unsuitable.map((misfit) => (
        <Group key={misfit.agentId} gap="xs">
          <AppLink to="/agents/$agentId" params={{ agentId: misfit.agentId }}>
            <Mono>{misfit.hostname}</Mono>
          </AppLink>
          {misfit.reasons.map((reason) => (
            <Badge key={reason} color={tones.warning}>
              {verifierReasonText(reason, i18n.language)}
            </Badge>
          ))}
        </Group>
      ))}
    </Stack>
  )
}

function Facts({ record }: { record: Verification }) {
  const { t, i18n } = useTranslation()
  const source = useQuery(sourceQuery(record.sourceId))
  return (
    <Table>
      <Table.Tbody>
        <Table.Tr>
          <Table.Th>{t('verification.record.source')}</Table.Th>
          <Table.Td>
            <AppLink to="/sources/$sourceId" params={{ sourceId: record.sourceId }}>
              <Mono>{source.data?.name ?? record.sourceId.slice(0, SHORT_ID)}</Mono>
            </AppLink>
          </Table.Td>
        </Table.Tr>
        <Table.Tr>
          <Table.Th>{t('verification.tab.columns.snapshot')}</Table.Th>
          <Table.Td>
            <Mono>{record.resticSnapshotId}</Mono>
          </Table.Td>
        </Table.Tr>
        <Table.Tr>
          <Table.Th>{t('verification.tab.columns.verifier')}</Table.Th>
          <Table.Td>
            {record.verifierAgentId === null ? '—' : <AgentName agentId={record.verifierAgentId} />}
          </Table.Td>
        </Table.Tr>
        <Table.Tr>
          <Table.Th>{t('verification.tab.columns.level')}</Table.Th>
          <Table.Td>{record.level === 0 ? '—' : levelWords(record.level, i18n.language)}</Table.Td>
        </Table.Tr>
        <Table.Tr>
          <Table.Th>{t('verification.tab.columns.restoreTime')}</Table.Th>
          <Table.Td>
            <Mono>{restoreTimeText(record.restoreDurationMs, i18n.language)}</Mono>
          </Table.Td>
        </Table.Tr>
        <Table.Tr>
          <Table.Th>{t('verification.record.keyHolders')}</Table.Th>
          <Table.Td>
            <Mono>{record.keyHolders}</Mono>
            {record.keyHolders === 1 && (
              <Text span c={tones.warning}>
                {' '}
                {t('verification.record.singleKey')}
              </Text>
            )}
          </Table.Td>
        </Table.Tr>
      </Table.Tbody>
    </Table>
  )
}

// A failure and a check that did not take place are two different pages of bad news (V-D10).
function Headline({ record }: { record: Verification }) {
  const { t } = useTranslation()
  const format = useFormat()
  return (
    <Stack gap="xs">
      <Group>
        <OutcomeBadge status={record.status} />
        <Time value={record.verifiedAt} />
      </Group>
      {record.status === 'infrastructure_failure' && (
        <Alert color={tones.warning} role="status">
          {t('verification.record.infrastructure')}{' '}
          {record.verifierAgentId !== null && (
            <AppLink to="/agents/$agentId" params={{ agentId: record.verifierAgentId }}>
              {t('verification.record.openVerifier')}
            </AppLink>
          )}
        </Alert>
      )}
      {record.cancelledBy !== null && record.cancelledAt !== null && (
        <Text>
          {t('verification.tab.cancelledBy', {
            who: record.cancelledBy,
            time: format.time(record.cancelledAt),
          })}
        </Text>
      )}
      {record.reason !== null && (
        <Text>
          {t('verification.record.reason')}: <Mono>{record.reason}</Mono>
        </Text>
      )}
      {record.stages.some(
        (stage) => stage.name === 'fingerprint_tag' && stage.status === 'failed',
      ) && (
        <Alert color={tones.error} role="status">
          {t('verification.record.tagMismatch')}
        </Alert>
      )}
    </Stack>
  )
}

function RecordCard({ record }: { record: Verification }) {
  const { t } = useTranslation()
  return (
    <Stack>
      <Title order={2}>{t('verification.record.title')}</Title>
      <Headline record={record} />
      <Mismatches record={record} />
      <Unsuitable record={record} />
      <Facts record={record} />
      <Fingerprint record={record} />
      <Card withBorder>
        <Title order={5}>{t('verification.record.stages')}</Title>
        <Stages stages={record.stages} />
      </Card>
    </Stack>
  )
}

export function VerificationPage() {
  const { t } = useTranslation()
  const { verificationId } = route.useParams()
  const record = useQuery(verificationQuery(verificationId))
  if (isNotFound(record.error)) {
    return (
      <Stack align="flex-start">
        <Text role="alert">{t('verification.record.notFound')}</Text>
        <AppLink to="/">{t('dashboard.title')}</AppLink>
      </Stack>
    )
  }
  return <Loaded query={record}>{(data) => <RecordCard record={data} />}</Loaded>
}
