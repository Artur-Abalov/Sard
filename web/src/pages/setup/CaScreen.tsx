// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Stack, Text, Title } from '@mantine/core'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { CopyBox } from '../../components/CopyBox'
import { Mono } from '../../components/Mono'
import { NoticeAlert } from '../../components/NoticeAlert'
import { confirmCa } from '../../onboarding/api'
import { caStepResult, type Notice, type StepResult } from '../../onboarding/outcomes'
import type { Onboarding } from '../../onboarding/state'
import { tones } from '../../theme'

const BACKUP_DOC = 'docs/operator/06-data-and-backup.md'
const MIGRATE_DOC = 'docs/operator/08-migrate-and-remove.md'

// How to bring one's own CA: shown only while the server says the CA can still be replaced.
function Replacement() {
  const { t } = useTranslation()
  return (
    <Stack gap="xs">
      <Title order={4}>{t('setup.ca.replaceTitle')}</Title>
      <Text>{t('setup.ca.replaceSteps', { doc: MIGRATE_DOC })}</Text>
      <CopyBox value={t('setup.ca.replaceCompose')} label="" />
      <Alert color={tones.warning}>{t('setup.ca.replaceWarning')}</Alert>
    </Stack>
  )
}

// The CA exactly as the server describes it, and the one decision of this step.
export function CaScreen({
  onboarding,
  onResult,
}: {
  onboarding: Onboarding
  onResult: (result: StepResult) => void
}) {
  const { t } = useTranslation()
  const [submitting, setSubmitting] = useState(false)
  const [notice, setNotice] = useState<Notice | null>(null)
  const { ca, caReplaceable } = onboarding

  async function confirm() {
    setSubmitting(true)
    const result = caStepResult(await confirmCa())
    setSubmitting(false)
    setNotice(result.kind === 'stay' ? result.notice : null)
    onResult(result)
  }

  if (ca === null) return null
  return (
    <Stack>
      <Title order={3}>{t('setup.ca.title')}</Title>
      <Text>{t('setup.ca.intro')}</Text>
      {notice && <NoticeAlert notice={notice} />}
      <CopyBox value={ca.fingerprint} label={t('setup.ca.fingerprint')} unbroken />
      <Text>
        {t('setup.ca.origin')}:{' '}
        {t(ca.origin === 'imported' ? 'setup.ca.originImported' : 'setup.ca.originGenerated')}
      </Text>
      <Text>
        {t('setup.ca.keyPath')}: <Mono>{ca.keyPath}</Mono>
      </Text>
      <Text>{t('setup.ca.backup', { doc: BACKUP_DOC })}</Text>
      {caReplaceable === true ? (
        <Replacement />
      ) : (
        <Text>{t('setup.ca.migrateOnly', { doc: MIGRATE_DOC })}</Text>
      )}
      <Button onClick={() => void confirm()} disabled={submitting} loading={submitting}>
        {t('setup.ca.submit')}
      </Button>
    </Stack>
  )
}
