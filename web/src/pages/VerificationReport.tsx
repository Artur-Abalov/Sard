// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Button, Group, Stack, Table, Text, TextInput, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { ApiError, call } from '../api/call'
import { client } from '../api/client'
import type { components } from '../api/schema'
import { AppLink } from '../components/links'
import { Loaded } from '../components/Loaded'
import { fieldFailures } from '../errors'
import { reportPeriodText, reportTitle } from '../report'
import { useFormat } from '../useFormat'
import { levelWords, outcomeView, restoreTimeText } from '../verification'

const route = getRouteApi('/sources_/$sourceId/verification-report')

type Report = components['schemas']['VerificationReportResponse']

const SHORT_ID = 8

// The words of the server's refusal at the field of the period it names.
function periodError(error: unknown, field: 'from' | 'to', text: string): string | undefined {
  const failures = error instanceof ApiError ? fieldFailures(error.body) : []
  return failures.some((failure) => failure.field === field) ? text : undefined
}

function Period({ sourceId, error }: { sourceId: string; error: unknown }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { from, to } = route.useSearch()
  function change(next: { from?: string; to?: string }) {
    void navigate({
      to: '/sources/$sourceId/verification-report',
      params: { sourceId },
      search: { from, to, ...next },
      replace: true,
    })
  }
  return (
    <Group align="flex-end" data-no-print>
      <TextInput
        type="date"
        label={t('report.from')}
        value={from ?? ''}
        onChange={(event) => change({ from: event.currentTarget.value || undefined })}
        error={periodError(error, 'from', t('errors.validation_failed'))}
      />
      <TextInput
        type="date"
        label={t('report.to')}
        value={to ?? ''}
        onChange={(event) => change({ to: event.currentTarget.value || undefined })}
        error={periodError(error, 'to', t('errors.validation_failed'))}
      />
      <Button onClick={() => window.print()}>{t('report.print')}</Button>
    </Group>
  )
}

function Items({ report }: { report: Report }) {
  const { t, i18n } = useTranslation()
  const format = useFormat()
  return (
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
        {report.items.map((item) => (
          <Table.Tr key={item.id}>
            <Table.Td>{format.time(item.verifiedAt)}</Table.Td>
            <Table.Td>{item.resticSnapshotId.slice(0, SHORT_ID)}</Table.Td>
            <Table.Td>{item.verifierHostname ?? '—'}</Table.Td>
            <Table.Td>
              {outcomeView(item.status, i18n.language).label}
              {item.cancelledBy === null ? '' : ` (${item.cancelledBy})`}
            </Table.Td>
            <Table.Td>{item.level === 0 ? '—' : levelWords(item.level, i18n.language)}</Table.Td>
            <Table.Td>{restoreTimeText(item.durationMs, i18n.language)}</Table.Td>
            <Table.Td>{restoreTimeText(item.restoreDurationMs, i18n.language)}</Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  )
}

function Failures({ report }: { report: Report }) {
  const { t } = useTranslation()
  const format = useFormat()
  if (report.failures.length === 0) return <Text>{t('report.noFailures')}</Text>
  return (
    <Table>
      <Table.Thead>
        <Table.Tr>
          <Table.Th>{t('report.failureStarted')}</Table.Th>
          <Table.Th>{t('report.failureRecords')}</Table.Th>
          <Table.Th>{t('report.failureFixed')}</Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {report.failures.map((failure) => (
          <Table.Tr key={failure.startedAt}>
            <Table.Td>{format.time(failure.startedAt)}</Table.Td>
            <Table.Td>{failure.records}</Table.Td>
            <Table.Td>
              {failure.fixedAt === null ? t('report.notFixed') : format.time(failure.fixedAt)}
            </Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  )
}

// The report of a source's verifications for a period, for print (rule 20): plain text and tables, no colors, no
// menu, no secrets or evidence; it ends with when it was made and by which version. The product's name is read from
// the one place the console has it.
function ReportBody({ report }: { report: Report }) {
  const { t, i18n } = useTranslation()
  const format = useFormat()
  const { summary } = report
  return (
    <Stack>
      <Title order={1}>{reportTitle(report.sourceName, i18n.language)}</Title>
      <Text>{reportPeriodText(report.from, report.to, report.timezone, i18n.language)}</Text>
      <Text>
        {t('report.summary', {
          total: summary.total,
          passed: summary.passed,
          failed: summary.failed,
          notHeld: summary.notHeld,
          cancelled: summary.cancelled,
        })}
      </Text>
      {report.items.length === 0 ? <Text>{t('report.empty')}</Text> : <Items report={report} />}
      <Title order={3}>{t('report.failures')}</Title>
      <Failures report={report} />
      <Text size="sm">
        {t('report.generated', {
          time: format.time(report.generatedAt),
          version: report.serverVersion,
        })}
      </Text>
    </Stack>
  )
}

export function VerificationReport() {
  const { t } = useTranslation()
  const { sourceId } = route.useParams()
  const { from, to } = route.useSearch()
  const report = useQuery({
    queryKey: ['verification-report', sourceId, from ?? null, to ?? null],
    queryFn: () =>
      call(
        client.GET('/api/v1/sources/{sourceId}/verification-report', {
          params: { path: { sourceId }, query: { from, to } },
        }),
      ),
    retry: false,
  })
  return (
    <Stack p="md" maw={900} mx="auto">
      <Group data-no-print>
        <AppLink to="/sources/$sourceId" params={{ sourceId }} search={{ tab: 'verifications' }}>
          {t('report.back')}
        </AppLink>
      </Group>
      <Period sourceId={sourceId} error={report.error} />
      <Loaded query={report}>{(data) => <ReportBody report={data} />}</Loaded>
    </Stack>
  )
}
