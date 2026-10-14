// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Button, Group, Stack, Text, Title } from '@mantine/core'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { selfBackupQuery } from '../../api/queries'
import type { components } from '../../api/schema'
import { ButtonLink } from '../../components/links'
import { Loaded } from '../../components/Loaded'
import { Mono } from '../../components/Mono'
import { useFormat } from '../../useFormat'
import { ChooseStorage } from '../setup/ChooseStorage'
import { FirstBackup } from '../setup/FirstBackup'
import { KeysScreen } from '../setup/KeysScreen'

type SelfBackup = components['schemas']['SelfBackup']

// The change of the storage repeats steps 3 and 4 of the wizard with the same screens (Рк17, OQ-206): the choice of
// the storage, the command and the wait for the repository, the first backup, the confirmation of the key copy.
function Change({ state, onFinished }: { state: SelfBackup; onFinished: () => void }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [bound, setBound] = useState(false)
  async function finished() {
    await queryClient.invalidateQueries({ queryKey: ['self-backup'] })
    onFinished()
  }
  if (!bound) {
    return (
      <Stack>
        <Text>{t('settings.selfBackup.changeIntro')}</Text>
        <ChooseStorage state={state} offerExisting={false} onBound={() => setBound(true)} />
      </Stack>
    )
  }
  if (!state.firstBackupDone) return <FirstBackup />
  return (
    <KeysScreen
      cardTo="/settings/recovery-card"
      onConfirmed={() => void finished()}
      onSelfBackupPending={() => setBound(true)}
    />
  )
}

function Current({ state }: { state: SelfBackup }) {
  const { t } = useTranslation()
  const format = useFormat()
  const [changing, setChanging] = useState(false)
  if (changing) {
    return (
      <Stack>
        <Change state={state} onFinished={() => setChanging(false)} />
        <Group>
          <Button variant="default" onClick={() => setChanging(false)}>
            {t('settings.selfBackup.cancel')}
          </Button>
        </Group>
      </Stack>
    )
  }
  return (
    <Stack>
      {state.repository ? (
        <>
          <Text>
            {t('settings.selfBackup.current', {
              name: state.repository.name,
              backend: state.repository.backend,
            })}{' '}
            <Mono>{state.repository.repositoryId}</Mono>
          </Text>
          <Text>
            {state.keysConfirmedAt === null
              ? t('settings.selfBackup.keysUnconfirmed')
              : t('settings.selfBackup.keysConfirmedAt', {
                  time: format.time(state.keysConfirmedAt),
                })}
          </Text>
        </>
      ) : (
        <Text>{t('settings.selfBackup.notConfigured')}</Text>
      )}
      <Group>
        <Button variant="default" onClick={() => setChanging(true)}>
          {t('settings.selfBackup.change')}
        </Button>
        {state.repository && (
          <ButtonLink to="/settings/recovery-card" variant="default">
            {t('settings.selfBackup.card')}
          </ButtonLink>
        )}
      </Group>
    </Stack>
  )
}

// The section "Self-backup" of the settings (Рк17): the repository, whether its key copy is confirmed, the recovery
// card and the change of the storage.
export function SelfBackupSection() {
  const { t } = useTranslation()
  const query = useQuery(selfBackupQuery())
  return (
    <Stack maw={640}>
      <Title order={3}>{t('settings.selfBackup.title')}</Title>
      <Loaded query={query}>{(state) => <Current state={state} />}</Loaded>
    </Stack>
  )
}
