// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Badge, Card, Group, SimpleGrid, Stack, Table, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { overviewQuery } from '../api/queries'
import type { components } from '../api/schema'
import { tones } from '../theme'
import { EmptyState } from './EmptyState'
import { AppLink } from './links'
import { Mono } from './Mono'
import { OutcomeBadge } from './OutcomeBadge'
import { VerificationMetric } from './VerificationMetric'
import { VerifierInstruction } from './VerifierInstruction'

type Block = components['schemas']['VerificationOverview']

// The figure of the summary: the number and what it counts. A bad one is told by its role and by its label.
function Figure({ value, label, tone }: { value: number; label: string; tone?: string }) {
  return (
    <Stack gap={0}>
      <Text size="xl" fw={600} c={value > 0 ? tone : undefined}>
        {value}
      </Text>
      <Text size="sm" c="dimmed">
        {label}
      </Text>
    </Stack>
  )
}

function Attention({ items }: { items: Block['attention'] }) {
  const { t } = useTranslation()
  return (
    <Table>
      <Table.Thead>
        <Table.Tr>
          <Table.Th>{t('sources.name')}</Table.Th>
          <Table.Th>{t('verification.overview.outcome')}</Table.Th>
          <Table.Th>{t('verification.metric.title')}</Table.Th>
        </Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {items.map((item) => (
          <Table.Tr key={item.sourceId}>
            <Table.Td>
              <AppLink to="/sources/$sourceId" params={{ sourceId: item.sourceId }}>
                <Mono>{item.sourceName}</Mono>
              </AppLink>
            </Table.Td>
            <Table.Td>
              <Group gap="xs">
                {item.status === null ? (
                  <Badge color={tones.neutral}>{t('verification.overview.neverVerified')}</Badge>
                ) : (
                  <OutcomeBadge status={item.status} />
                )}
                {item.staleSince !== null && (
                  <Badge color={tones.warning}>{t('verification.overview.stale')}</Badge>
                )}
              </Group>
            </Table.Td>
            <Table.Td>
              <VerificationMetric
                enabled
                lastVerifiedAt={item.lastVerifiedAt}
                lastVerifiedLevel={item.lastVerifiedLevel}
              />
            </Table.Td>
          </Table.Tr>
        ))}
      </Table.Tbody>
    </Table>
  )
}

// The summary of the installation (rule 2): the server counts and orders; this only shows. The two kinds of bad
// news have counters of their own: a failed backup is an error, a check that did not take place a warning.
export function VerificationSummary({ block }: { block: Block }) {
  const { t } = useTranslation()
  const nothing = block.enabled + block.disabled === 0
  return (
    <Card withBorder>
      <Stack>
        <Title order={4}>{t('verification.overview.title')}</Title>
        {nothing ? (
          <EmptyState text={t('verification.overview.noSources')} />
        ) : (
          <>
            <SimpleGrid cols={{ base: 2, sm: 3, md: 6 }}>
              <Figure
                value={block.verifiedLastDay}
                label={t('verification.overview.verifiedLastDay')}
                tone={tones.success}
              />
              <Figure
                value={block.failed}
                label={t('verification.overview.failed')}
                tone={tones.error}
              />
              <Figure
                value={block.notHeld}
                label={t('verification.overview.notHeld')}
                tone={tones.warning}
              />
              <Figure
                value={block.stale}
                label={t('verification.overview.stale')}
                tone={tones.warning}
              />
              <Figure
                value={block.neverVerified}
                label={t('verification.overview.neverVerified')}
              />
              <Figure value={block.disabled} label={t('verification.overview.disabled')} />
            </SimpleGrid>
            {block.attention.length > 0 && (
              <>
                <Title order={5}>{t('verification.overview.attention')}</Title>
                <Attention items={block.attention} />
              </>
            )}
          </>
        )}
      </Stack>
    </Card>
  )
}

// Without a verifier restore verification does not work, and the owner is told so until there is one (rule 13):
// a warning without a close button, with the instruction to set one up.
export function NoVerifierNotice() {
  const { t } = useTranslation()
  const overview = useQuery(overviewQuery())
  if (overview.data?.verification.noVerifier !== true) return null
  return (
    <Stack>
      <Alert color={tones.warning} role="alert" title={t('verification.noVerifier.title')}>
        {t('verification.noVerifier.text')}
      </Alert>
      <VerifierInstruction />
    </Stack>
  )
}
