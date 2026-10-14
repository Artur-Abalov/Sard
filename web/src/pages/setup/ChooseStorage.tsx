// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Checkbox, Stack, Text } from '@mantine/core'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { components } from '../../api/schema'
import { NoticeAlert } from '../../components/NoticeAlert'
import { attachRepository } from '../../onboarding/selfBackup'
import { tones } from '../../theme'
import { CommandStage } from './CommandStage'
import { StorageChoice } from './StorageChoice'

type SelfBackup = components['schemas']['SelfBackup']
type Command = components['schemas']['RepositoryCommand']

// A repository the neighbour already initialised (the owner closed the tab after the command): bind it with one
// button, a local one only after the owner understood what it means (Рк11).
function AttachOffer({
  repository,
  onBound,
  onOther,
}: {
  repository: SelfBackup['repositories'][number]
  onBound: () => void
  onOther: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [understood, setUnderstood] = useState(false)
  const [failed, setFailed] = useState(false)

  async function attach() {
    const result = await attachRepository(repository, understood)
    setFailed(result !== 'bound')
    if (result === 'bound') {
      await queryClient.invalidateQueries({ queryKey: ['self-backup'] })
      await queryClient.invalidateQueries({ queryKey: ['onboarding'] })
      onBound()
    }
  }

  return (
    <Stack>
      <Text fw={500}>{t('setup.selfBackup.attach.title')}</Text>
      <Text>{t('setup.selfBackup.attach.message', { name: repository.name })}</Text>
      {repository.local && (
        <Alert color={tones.warning} role="alert">
          <Checkbox
            checked={understood}
            onChange={(event) => setUnderstood(event.currentTarget.checked)}
            label={t('setup.selfBackup.local.warning')}
          />
        </Alert>
      )}
      {failed && <NoticeAlert notice={{ key: 'setup.selfBackup.bindFailed' }} />}
      <Button onClick={() => void attach()} disabled={repository.local && !understood}>
        {t('setup.selfBackup.attach.submit', { name: repository.name })}
      </Button>
      <Button variant="default" onClick={onOther}>
        {t('setup.selfBackup.attach.other')}
      </Button>
    </Stack>
  )
}

// The way to a bound repository: the choice of the storage, the command for the terminal of the server, the wait for
// the repository it adds and its binding. [offerExisting] first offers a repository the neighbour already has.
export function ChooseStorage({
  state,
  offerExisting,
  onBound,
}: {
  state: SelfBackup
  offerExisting: boolean
  onBound: () => void
}) {
  const [command, setCommand] = useState<{ command: Command; understood: boolean } | null>(null)
  const [other, setOther] = useState(false)
  const existing = state.repositories.find((r) => r.repositoryId !== null)

  if (command !== null) {
    return (
      <CommandStage
        command={command.command}
        localUnderstood={command.understood}
        onBack={() => setCommand(null)}
        onBound={onBound}
      />
    )
  }
  if (offerExisting && existing !== undefined && !other) {
    return <AttachOffer repository={existing} onBound={onBound} onOther={() => setOther(true)} />
  }
  return (
    <StorageChoice onCommand={(next, understood) => setCommand({ command: next, understood })} />
  )
}
