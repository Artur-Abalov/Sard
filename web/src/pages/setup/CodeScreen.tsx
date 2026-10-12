// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Button, Stack, Text, TextInput, Title } from '@mantine/core'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { CopyBox } from '../../components/CopyBox'
import { NoticeAlert } from '../../components/NoticeAlert'
import { enterCode } from '../../onboarding/api'
import { codeResult, type Notice, type StepResult } from '../../onboarding/outcomes'

// The setup code from the log of the server. The code goes to the server as typed and is
// kept nowhere but in this field (not in the address, not in browser storage).
export function CodeScreen({
  notice,
  onResult,
}: {
  notice: Notice | null
  onResult: (result: StepResult) => void
}) {
  const { t } = useTranslation()
  const [code, setCode] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [own, setOwn] = useState<Notice | null>(null)

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (code === '') return
    setSubmitting(true)
    const result = codeResult(await enterCode(code))
    setSubmitting(false)
    setOwn(result.kind === 'stay' ? result.notice : null)
    if (result.kind !== 'stay') setCode('')
    onResult(result)
  }

  const shown = own ?? notice
  return (
    <form onSubmit={(event) => void submit(event)}>
      <Stack>
        <Title order={3}>{t('setup.code.title')}</Title>
        {shown && <NoticeAlert notice={shown} />}
        <Text>{t('setup.code.hint')}</Text>
        <CopyBox value={t('setup.code.command')} label="" />
        <TextInput
          label={t('setup.code.label')}
          value={code}
          onChange={(event) => setCode(event.currentTarget.value)}
          autoComplete="off"
          spellCheck={false}
          ff="monospace"
          data-autofocus
        />
        <Button type="submit" disabled={code === '' || submitting} loading={submitting}>
          {t('setup.code.submit')}
        </Button>
      </Stack>
    </form>
  )
}
