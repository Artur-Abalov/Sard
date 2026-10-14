// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Stack, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { selfBackupQuery } from '../../api/queries'
import type { components } from '../../api/schema'
import { Loaded } from '../../components/Loaded'
import { Mono } from '../../components/Mono'
import { ChooseStorage } from './ChooseStorage'
import { FirstBackup } from './FirstBackup'
import { PlanBlock } from './PlanBlock'

type SelfBackup = components['schemas']['SelfBackup']

// How often the wizard looks at the neighbour while it waits for a repository (Рк11).
const WIZARD_POLL_MS = 5000

function Bound({ state }: { state: SelfBackup }) {
  const { t } = useTranslation()
  return (
    <Stack>
      {state.repository && (
        <Text>
          {t('settings.selfBackup.current', {
            name: state.repository.name,
            backend: state.repository.backend,
          })}{' '}
          <Mono>{state.repository.repositoryId}</Mono>
        </Text>
      )}
      <PlanBlock state={state} />
      <FirstBackup />
    </Stack>
  )
}

// Step 3 of the wizard: the storage of the backup of the server itself, what goes into it, its schedule and the
// first backup (Рк11–Рк13). The server's state decides what shows: a bound self-backup goes on to the plan and the
// first backup, an unbound one to the choice of the storage.
export function SelfBackupScreen() {
  const { t } = useTranslation()
  const query = useQuery({ ...selfBackupQuery(), refetchInterval: WIZARD_POLL_MS })
  return (
    <Stack>
      <Title order={3}>{t('setup.selfBackup.title')}</Title>
      <Text>{t('setup.selfBackup.intro')}</Text>
      <Loaded query={query}>
        {(state) =>
          state.configured ? (
            <Bound state={state} />
          ) : (
            <ChooseStorage state={state} offerExisting onBound={() => undefined} />
          )
        }
      </Loaded>
    </Stack>
  )
}
