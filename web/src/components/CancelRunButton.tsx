// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Button, Stack, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { call } from '../api/call'
import { client } from '../api/client'
import { runQuery } from '../api/queries'
import type { components } from '../api/schema'
import { cancellable } from '../runOutcome'
import { tones } from '../theme'
import { ConfirmModal } from './ConfirmModal'
import { ErrorBlock } from './ErrorBlock'

type Run = Pick<components['schemas']['RunSummary'], 'id' | 'kind' | 'outcome' | 'trigger'>

// "Cancel" for an active run: one dialog, and only its confirmation sends the request. The answer of the
// server is the run as shown from then on; a refusal is told by its code and the run is read again.
// The component stays while the run is final, so that the refusal of a late cancel stays on the screen.
export function CancelRunButton({ run }: { run: Run }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [opened, { open, close }] = useDisclosure()
  const cancel = useMutation({
    mutationFn: () =>
      call(client.POST('/api/v1/runs/{runId}/cancel', { params: { path: { runId: run.id } } })),
    onSuccess: (answer) => queryClient.setQueryData(runQuery(run.id).queryKey, answer),
    onSettled: async () => {
      close()
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['runs'] }),
        queryClient.invalidateQueries({ queryKey: ['overview'] }),
        queryClient.invalidateQueries({ queryKey: runQuery(run.id).queryKey }),
      ])
    },
  })
  return (
    <Stack gap="xs" align="flex-start">
      {cancellable(run) && (
        <>
          <Button size="xs" variant="default" c={tones.error} onClick={open}>
            {t('run.cancel.button')}
          </Button>
          <ConfirmModal
            opened={opened}
            title={t('run.cancel.title')}
            confirmLabel={t('run.cancel.confirm')}
            busy={cancel.isPending}
            onConfirm={() => cancel.mutate()}
            onClose={close}
          >
            <Text>
              {run.kind === 'restore' ? t('run.cancel.restoreWarning') : t('run.cancel.backupText')}
            </Text>
          </ConfirmModal>
        </>
      )}
      {cancel.isError && <ErrorBlock error={cancel.error} />}
    </Stack>
  )
}
