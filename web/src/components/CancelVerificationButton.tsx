// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Stack, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { call, describeError } from '../api/call'
import { client } from '../api/client'
import { sourceQuery, sourceVerificationQuery } from '../api/queries'
import type { components } from '../api/schema'
import { tones } from '../theme'
import { jobCancelMessageKey, jobCancellable } from '../verification'
import { ConfirmModal } from './ConfirmModal'

type Job = components['schemas']['VerificationJob']

// «Cancel the verification» for the open job of a source: one dialog, and only its confirmation sends the request.
// The answer is the source's verification as it is from then on (the job, marked as being cancelled, until the
// verifier confirms); a refusal is told in the words of the verification and the tab is read again.
export function CancelVerificationButton({ sourceId, job }: { sourceId: string; job: Job }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [opened, { open, close }] = useDisclosure()
  const cancel = useMutation({
    mutationFn: () =>
      call(
        client.POST('/api/v1/verification-jobs/{jobId}/cancel', {
          params: { path: { jobId: job.id } },
        }),
      ),
    onSuccess: (answer) =>
      queryClient.setQueryData(sourceVerificationQuery(sourceId).queryKey, answer),
    onSettled: async () => {
      close()
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['verification', sourceId] }),
        queryClient.invalidateQueries({ queryKey: ['verifications', sourceId] }),
        queryClient.invalidateQueries({ queryKey: sourceQuery(sourceId).queryKey }),
        queryClient.invalidateQueries({ queryKey: ['overview'] }),
      ])
    },
  })
  const refusal = cancel.isError ? describeError(cancel.error) : null
  return (
    <Stack gap="xs" align="flex-start">
      {jobCancellable(job) && (
        <>
          <Button size="xs" variant="default" c={tones.error} onClick={open}>
            {t('verification.tab.cancel.button')}
          </Button>
          <ConfirmModal
            opened={opened}
            title={t('verification.tab.cancel.title')}
            confirmLabel={t('verification.tab.cancel.confirm')}
            busy={cancel.isPending}
            onConfirm={() => cancel.mutate()}
            onClose={close}
          >
            <Text>{t('verification.tab.cancel.text')}</Text>
          </ConfirmModal>
        </>
      )}
      {refusal !== null && (
        <Alert color={tones.error} role="alert">
          {t(jobCancelMessageKey(refusal.key), refusal.params)}
        </Alert>
      )}
    </Stack>
  )
}
