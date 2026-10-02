// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Card, Group, List, Stack, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { overviewQuery, recentRunsQuery } from '../api/queries'
import { fetchStatus } from '../api/client'
import { buildChecklist, type ChecklistItem } from '../checklist'
import { EmptyState } from '../components/EmptyState'
import { AppLink, ButtonLink } from '../components/links'
import { Loaded } from '../components/Loaded'
import { RunsTable } from '../components/RunsTable'
import { runListPollInterval } from '../polling'

const RECENT_RUNS = 10

// An unfinished step leads to the page where it is done.
function StepLink({ item, label }: { item: ChecklistItem; label: string }) {
  const { target } = item
  switch (target.to) {
    case '/tokens':
      return (
        <AppLink to="/tokens" search={target.search}>
          {label}
        </AppLink>
      )
    case '/agents':
      return (
        <AppLink to="/agents" search={target.search}>
          {label}
        </AppLink>
      )
    case '/sources':
      return (
        <AppLink to="/sources" search={target.search}>
          {label}
        </AppLink>
      )
    case '/sources/new':
      return <AppLink to="/sources/new">{label}</AppLink>
  }
}

function Checklist({ items }: { items: ChecklistItem[] }) {
  const { t } = useTranslation()
  return (
    <Card withBorder>
      <Title order={4}>{t('dashboard.firstSteps')}</Title>
      <List listStyleType="none" mt="xs">
        {items.map((item) => (
          <List.Item key={item.step}>
            <Group gap="xs">
              <Text span aria-hidden="true">
                {item.done ? '✔' : '○'}
              </Text>
              <Text span>
                {item.done ? (
                  t(`dashboard.steps.${item.step}`)
                ) : (
                  <StepLink item={item} label={t(`dashboard.steps.${item.step}`)} />
                )}
              </Text>
              <Text span size="sm" c="dimmed">
                {item.done ? t('dashboard.done') : t('dashboard.notDone')}
              </Text>
            </Group>
          </List.Item>
        ))}
      </List>
    </Card>
  )
}

// The counts and the checklist come from GET /overview alone: the console computes neither.
function OverviewBlocks() {
  const { t } = useTranslation()
  const overview = useQuery(overviewQuery())
  return (
    <Loaded query={overview}>
      {(data) => {
        const checklist = buildChecklist(data.firstSteps)
        return (
          <>
            <Card withBorder>
              <Title order={4}>{t('agents.title')}</Title>
              {data.agentsTotal === 0 ? (
                <EmptyState text={t('dashboard.noAgents')}>
                  <ButtonLink to="/tokens" search={{ create: true }}>
                    {t('dashboard.issueToken')}
                  </ButtonLink>
                </EmptyState>
              ) : (
                <AppLink to="/agents">
                  {t('dashboard.agentsOnline', {
                    online: data.agentsOnline,
                    total: data.agentsTotal,
                  })}
                </AppLink>
              )}
            </Card>
            {checklist && <Checklist items={checklist} />}
          </>
        )
      }}
    </Loaded>
  )
}

function RecentRuns() {
  const { t } = useTranslation()
  const runs = useQuery({
    ...recentRunsQuery(RECENT_RUNS),
    refetchInterval: (query) => runListPollInterval(query.state.data?.items),
  })
  return (
    <Card withBorder>
      <Group justify="space-between">
        <Title order={4}>{t('dashboard.recentRuns')}</Title>
        <AppLink to="/runs">{t('dashboard.allRuns')}</AppLink>
      </Group>
      <Loaded query={runs}>
        {(page) =>
          page.items.length === 0 ? (
            <EmptyState text={t('dashboard.noRuns')} />
          ) : (
            <RunsTable runs={page.items} />
          )
        }
      </Loaded>
    </Card>
  )
}

function ServerVersion() {
  const { t } = useTranslation()
  const status = useQuery({ queryKey: ['status'], queryFn: fetchStatus })
  if (status.data === undefined) return null
  return (
    <Text c="dimmed" size="sm">
      {t('dashboard.serverVersion', { version: status.data.version })}
    </Text>
  )
}

export function Dashboard() {
  const { t } = useTranslation()
  return (
    <Stack>
      <Title order={2}>{t('dashboard.title')}</Title>
      <Group>
        <ButtonLink to="/tokens" search={{ create: true }}>
          {t('dashboard.issueToken')}
        </ButtonLink>
        <ButtonLink to="/sources/new" variant="default">
          {t('dashboard.createSource')}
        </ButtonLink>
      </Group>
      <OverviewBlocks />
      <RecentRuns />
      <ServerVersion />
    </Stack>
  )
}
