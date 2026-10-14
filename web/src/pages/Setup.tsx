// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Card, Group, Stack, Text, Title } from '@mantine/core'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Loaded } from '../components/Loaded'
import { onboardingQuery } from '../onboarding/api'
import type { Notice, StepResult } from '../onboarding/outcomes'
import { screenOf, type Onboarding } from '../onboarding/state'
import { AdminScreen } from './setup/AdminScreen'
import { CaScreen } from './setup/CaScreen'
import { CodeScreen } from './setup/CodeScreen'
import { KeysScreen } from './setup/KeysScreen'
import { RestartScreen } from './setup/RestartScreen'
import { SelfBackupScreen } from './setup/SelfBackupScreen'
import { StepList } from './setup/StepList'

// How often the wizard asks the server how far it has come: the self-backup step is done by runs the server watches.
const ONBOARDING_POLL_MS = 5000

function Screen({
  onboarding,
  notice,
  onResult,
  onConfirmed,
  onPending,
}: {
  onboarding: Onboarding
  notice: Notice | null
  onResult: (result: StepResult) => void
  onConfirmed: () => void
  onPending: () => void
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
    case 'selfBackup':
      return <SelfBackupScreen />
    case 'keys':
      return (
        <KeysScreen
          cardTo="/setup/recovery-card"
          onConfirmed={onConfirmed}
          onSelfBackupPending={onPending}
        />
      )
    case 'complete':
      return null
  }
}

// The first-start wizard (Рк1..Рк5, Рк10). Which screen shows follows the server's answer to
// GET /api/v1/onboarding; a step's own answer decides whether to go on, back or to sign-in. The steps of the
// self-backup and the key copy follow the server's state, which the wizard asks for again while it waits.
export function Setup() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const query = useQuery({ ...onboardingQuery(), refetchInterval: ONBOARDING_POLL_MS })
  const [notice, setNotice] = useState<Notice | null>(null)

  // Nothing is left to do (the onboarding was completed elsewhere): the console opens.
  const complete = query.data !== undefined && screenOf(query.data) === 'complete'
  useEffect(() => {
    if (complete) void navigate({ to: '/' })
  }, [complete, navigate])

  // The key copy is confirmed: the onboarding is complete and the console opens (Рк14).
  async function onConfirmed() {
    queryClient.removeQueries({ queryKey: ['onboarding'] })
    await navigate({ to: '/' })
  }

  // The server says the first backups are not done (first_backup_pending): back to the step where they are.
  async function onPending() {
    await queryClient.invalidateQueries({ queryKey: ['onboarding'] })
  }

  async function onResult(result: StepResult) {
    if (result.kind === 'stay') return
    if (result.kind === 'goto' && result.target === 'login') {
      await navigate({ to: '/login', search: { notice: 'setup_completed' } })
      return
    }
    if (result.kind === 'done' && query.data !== undefined && screenOf(query.data) === 'admin') {
      // The password step signed the owner in: the wizard goes on with the self-backup (Рк10), or, when the
      // server asks nothing more of this installation, straight to the console, no sign-in page.
      await queryClient.invalidateQueries({ queryKey: ['onboarding'] })
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
                  <Screen
                    onboarding={onboarding}
                    notice={notice}
                    onResult={onResult}
                    onConfirmed={() => void onConfirmed()}
                    onPending={() => void onPending()}
                  />
                </Stack>
              </Group>
            )}
          </Loaded>
        </Stack>
      </Card>
    </Stack>
  )
}
