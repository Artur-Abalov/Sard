// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Checkbox, Stack, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { selfBackupQuery } from '../../api/queries'
import { CopyBox } from '../../components/CopyBox'
import { Loaded } from '../../components/Loaded'
import { ButtonLink } from '../../components/links'
import { confirmKeysCopy } from '../../onboarding/selfBackup'
import { tones } from '../../theme'

// Step 4 of the wizard, and the confirmation after a change of the storage (Рк14, Рк17, V3 КВ1): the command that
// issues a recovery key on the machine of the server first, below it the command that prints the password of the
// repository, the recovery card, the owner's mark and the button that is active only with it. No password passes
// through the console (КВ6). Every text is a key of setup.keys.
export function KeysScreen({
  cardTo,
  onConfirmed,
  onSelfBackupPending,
}: {
  cardTo: '/setup/recovery-card' | '/settings/recovery-card'
  onConfirmed: () => void
  onSelfBackupPending: () => void
}) {
  const { t } = useTranslation()
  const query = useQuery(selfBackupQuery())
  const [marked, setMarked] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [failed, setFailed] = useState(false)

  async function finish() {
    setSubmitting(true)
    setFailed(false)
    const result = await confirmKeysCopy()
    setSubmitting(false)
    if (result === 'done') onConfirmed()
    else if (result === 'first_backup_pending') onSelfBackupPending()
    else setFailed(true)
  }

  return (
    <Stack>
      <Title order={3}>{t('setup.keys.title')}</Title>
      <Text>{t('setup.keys.intro')}</Text>
      <Loaded query={query}>
        {(state) => (
          <>
            {state.recoveryKeyCommand && (
              <>
                <Text>{t('setup.keys.recoveryCommand')}</Text>
                <CopyBox value={state.recoveryKeyCommand} label={t('setup.keys.commandLabel')} />
              </>
            )}
            {state.passwordRevealCommand && (
              <>
                <Text>{t('setup.keys.secondCommand')}</Text>
                <CopyBox value={state.passwordRevealCommand} label={t('setup.keys.commandLabel')} />
              </>
            )}
          </>
        )}
      </Loaded>
      <Text>{t('setup.keys.storage')}</Text>
      <Text>{t('setup.keys.card')}</Text>
      <ButtonLink to={cardTo} variant="default">
        {t('setup.keys.cardLink')}
      </ButtonLink>
      <Checkbox
        checked={marked}
        onChange={(event) => setMarked(event.currentTarget.checked)}
        label={t('setup.keys.confirm')}
      />
      {failed && (
        <Alert color={tones.error} role="alert">
          {t('setup.keys.unavailable')}
        </Alert>
      )}
      <Button disabled={!marked || submitting} loading={submitting} onClick={() => void finish()}>
        {t('setup.keys.submit')}
      </Button>
    </Stack>
  )
}
