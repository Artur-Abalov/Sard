// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Card, Group, Stack, Text, Title } from '@mantine/core'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loaded } from '../components/Loaded'
import { onboardingQuery } from '../onboarding/api'
import type { Notice, StepResult } from '../onboarding/outcomes'
import { screenOf, type Onboarding } from '../onboarding/state'
import { AdminScreen } from './setup/AdminScreen'
import { CaScreen } from './setup/CaScreen'
import { CodeScreen } from './setup/CodeScreen'
import { RestartScreen } from './setup/RestartScreen'
import { StepList } from './setup/StepList'

function Screen({
  onboarding,
  notice,
  onResult,
}: {
  onboarding: Onboarding
  notice: Notice | null
  onResult: (result: StepResult) => void
}) {
  switch (screenOf(onboarding)) {
    case 'code':
      return <CodeScreen notice={notice} onResult={onResult} />
    case 'restart':
      return <RestartScreen />
    case 'ca':
      return <CaScreen onboarding={onboarding} onResult={onResult} />
    case 'admin':
      return <AdminScreen onResult={onResult} />
  }
}

// The first-start wizard (Рк1..Рк5). Which screen shows follows the server's answer to
// GET /api/v1/onboarding; a step's own answer decides whether to go on, back or to sign-in.
export function Setup() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const query = useQuery(onboardingQuery())
  const [notice, setNotice] = useState<Notice | null>(null)

  async function onResult(result: StepResult) {
    if (result.kind === 'stay') return
    if (result.kind === 'goto' && result.target === 'login') {
      await navigate({ to: '/login', search: { notice: 'setup_completed' } })
      return
    }
    if (result.kind === 'done' && query.data !== undefined && screenOf(query.data) === 'admin') {
      // The password step signed the owner in: straight to the console, no sign-in page.
      queryClient.removeQueries({ queryKey: ['onboarding'] })
      await navigate({ to: '/' })
      return
    }
    setNotice(result.kind === 'goto' ? result.notice : null)
    await queryClient.invalidateQueries({ queryKey: ['onboarding'] })
  }

  return (
    <Stack align="center" mih="100vh" py="xl">
      <Card withBorder padding="lg" w="100%" maw={640}>
        <Stack>
          <Title order={2}>{t('setup.title')}</Title>
          <Text>{t('setup.intro')}</Text>
          <Loaded query={query}>
            {(onboarding) => (
              <Group align="flex-start" gap="xl" wrap="wrap">
                <StepList onboarding={onboarding} />
                <Stack style={{ flex: 1, minWidth: 280 }}>
                  <Screen onboarding={onboarding} notice={notice} onResult={onResult} />
                </Stack>
              </Group>
            )}
          </Loaded>
        </Stack>
      </Card>
    </Stack>
  )
}
