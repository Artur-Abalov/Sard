// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Checkbox, Code, Group, Loader, Stack, Text } from '@mantine/core'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { components } from '../../api/schema'
import { NoticeAlert } from '../../components/NoticeAlert'
import { bindWhenReady } from '../../onboarding/selfBackup'
import { tones } from '../../theme'

type Command = components['schemas']['RepositoryCommand']

// The neighbour is asked about once in this long; the console keeps asking until the repository is there (Рк11).
const WAIT_POLL_MS = 5000
// After this long without a repository the wizard says where to look for the error.
const SLOW_MS = 30_000

// The commands of the server in one block, in the order of the answer, and the wait for the repository they add.
// When the neighbour reports it with an id the console binds it itself (with the address, for the recovery card).
export function CommandStage({
  command,
  localUnderstood,
  onBack,
  onBound,
}: {
  command: Command
  localUnderstood: boolean
  onBack: () => void
  onBound: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [understood, setUnderstood] = useState(localUnderstood)
  const [slow, setSlow] = useState(false)
  const [copied, setCopied] = useState<'idle' | 'copied' | 'refused'>('idle')

  useEffect(() => {
    const timer = setTimeout(() => setSlow(true), SLOW_MS)
    return () => clearTimeout(timer)
  }, [])

  // One look at the neighbour is one request of this query; it asks again until the repository is bound.
  const look = useQuery({
    queryKey: ['bind-when-ready', command.repositoryName, command.address, understood],
    queryFn: async () => {
      const result = await bindWhenReady(command, understood)
      if (result === 'bound') {
        await queryClient.invalidateQueries({ queryKey: ['self-backup'] })
        await queryClient.invalidateQueries({ queryKey: ['onboarding'] })
        onBound()
      }
      return result
    },
    refetchInterval: (query) => (query.state.data === 'bound' ? false : WAIT_POLL_MS),
    retry: false,
    gcTime: 0,
  })
  const asksConfirmation = look.data === 'local_storage_unconfirmed'
  const failed = look.data === 'failed'

  async function copy() {
    try {
      await navigator.clipboard.writeText(command.commands.join('\n'))
      setCopied('copied')
    } catch {
      setCopied('refused')
    }
  }

  return (
    <Stack>
      <Text fw={500}>{t('setup.selfBackup.command.title')}</Text>
      <Text size="sm">{t('setup.selfBackup.command.hint')}</Text>
      <Group align="flex-start" wrap="nowrap">
        <Code block style={{ flex: 1, whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
          {command.commands.join('\n')}
        </Code>
        <Button variant="default" size="xs" onClick={() => void copy()}>
          {t('common.copy')}
        </Button>
      </Group>
      {copied === 'copied' && (
        <Text size="xs" role="status">
          {t('common.copied')}
        </Text>
      )}
      {copied === 'refused' && (
        <Text size="xs" c={tones.error} role="alert">
          {t('common.copyFailed')}
        </Text>
      )}
      {asksConfirmation && (
        <Alert color={tones.warning} role="alert">
          <Checkbox
            checked={understood}
            onChange={(event) => setUnderstood(event.currentTarget.checked)}
            label={t('setup.selfBackup.local.warning')}
          />
        </Alert>
      )}
      {failed && <NoticeAlert notice={{ key: 'setup.selfBackup.bindFailed' }} />}
      <Group gap="sm">
        <Loader size="xs" aria-hidden />
        <Text size="sm" role="status">
          {t('setup.selfBackup.wait.message')}
        </Text>
      </Group>
      {slow && (
        <Text size="sm" role="status">
          {t('setup.selfBackup.wait.slow', { command: t('setup.selfBackup.wait.logs') })}
        </Text>
      )}
      <Group>
        <Button variant="default" onClick={onBack}>
          {t('setup.selfBackup.command.back')}
        </Button>
      </Group>
    </Stack>
  )
}
