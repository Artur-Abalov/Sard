// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Checkbox, Radio, Select, Stack, Text, TextInput } from '@mantine/core'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { NoticeAlert } from '../../components/NoticeAlert'
import type { components } from '../../api/schema'
import {
  canShowCommand,
  emptyForm,
  fetchCommand,
  type Provider,
  type Storage,
  type StorageForm,
} from '../../onboarding/selfBackup'
import { tones } from '../../theme'

type Command = components['schemas']['RepositoryCommand']

const STORAGES: Storage[] = ['s3', 'sftp', 'local']
const PROVIDERS: Provider[] = ['aws', 'b2', 'custom']
const TROUBLESHOOTING = 'docs/operator/09-troubleshooting.md'

// The fields each part of the form shows: a label key of setup.selfBackup.field and the field of the form.
type FieldName = Exclude<keyof StorageForm, 'storage' | 'provider'>
const S3_FIELDS: Record<Provider, FieldName[]> = {
  aws: ['region', 'bucket', 'path'],
  b2: ['region', 'bucket', 'path'],
  custom: ['endpoint', 'bucket', 'path'],
}
const SFTP_FIELDS: FieldName[] = ['user', 'host', 'port', 'path', 'hostKeyFingerprint']

function labelKey(storage: Storage, field: FieldName): string {
  if (field === 'path' && storage === 'sftp') return 'setup.selfBackup.field.sftpPath'
  return `setup.selfBackup.field.${field}`
}

function visibleFields(form: StorageForm): FieldName[] {
  if (form.storage === 'sftp') return SFTP_FIELDS
  return form.storage === 's3' ? S3_FIELDS[form.provider] : []
}

// What the owner chose for the storage, and the command the server builds for it (Рк11). No field takes a key, a
// key ID or a password: the text says they are typed in the terminal of the server.
export function StorageChoice({
  onCommand,
}: {
  onCommand: (command: Command, localUnderstood: boolean) => void
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState<StorageForm>(emptyForm)
  const [understood, setUnderstood] = useState(false)
  const [refused, setRefused] = useState<string[]>([])
  const [failure, setFailure] = useState<'agent_missing' | 'unavailable' | null>(null)
  const [asking, setAsking] = useState(false)

  const set = (patch: Partial<StorageForm>) => setForm({ ...form, ...patch })

  async function show(event: React.FormEvent) {
    event.preventDefault()
    if (!canShowCommand(form, understood)) return
    setAsking(true)
    setRefused([])
    setFailure(null)
    const result = await fetchCommand(form)
    setAsking(false)
    if (result.kind === 'ok') onCommand(result.command, understood)
    else if (result.kind === 'fields') setRefused(result.fields)
    else setFailure(result.kind)
  }

  return (
    <form onSubmit={(event) => void show(event)}>
      <Stack>
        <Radio.Group
          label={t('setup.selfBackup.storage.title')}
          value={form.storage}
          onChange={(storage) => set({ storage: storage as Storage })}
        >
          <Stack gap="xs" mt="xs">
            {STORAGES.map((storage) => (
              <Radio
                key={storage}
                value={storage}
                label={t(`setup.selfBackup.storage.${storage}`)}
              />
            ))}
          </Stack>
        </Radio.Group>
        {form.storage === 's3' && (
          <Select
            label={t('setup.selfBackup.provider.label')}
            value={form.provider}
            allowDeselect={false}
            data={PROVIDERS.map((value) => ({
              value,
              label: t(`setup.selfBackup.provider.${value}`),
            }))}
            onChange={(provider) => set({ provider: (provider ?? 'aws') as Provider })}
          />
        )}
        {visibleFields(form).map((field) => (
          <TextInput
            key={field}
            label={t(labelKey(form.storage, field))}
            value={form[field]}
            onChange={(event) => set({ [field]: event.currentTarget.value })}
            error={refused.includes(field) ? t('errors.validation_failed') : null}
            autoComplete="off"
          />
        ))}
        {form.storage === 'local' && (
          <Alert color={tones.warning} role="alert">
            <Checkbox
              checked={understood}
              onChange={(event) => setUnderstood(event.currentTarget.checked)}
              label={t('setup.selfBackup.local.warning')}
            />
          </Alert>
        )}
        <Text size="sm">{t('setup.selfBackup.noSecrets')}</Text>
        {failure === 'agent_missing' && (
          <Alert color={tones.error} role="alert">
            {t('setup.selfBackup.command.agentMissing', { doc: TROUBLESHOOTING })}
          </Alert>
        )}
        {failure === 'unavailable' && (
          <NoticeAlert notice={{ key: 'setup.selfBackup.unavailable' }} />
        )}
        <Button
          type="submit"
          disabled={!canShowCommand(form, understood) || asking}
          loading={asking}
        >
          {t('setup.selfBackup.showCommand')}
        </Button>
      </Stack>
    </form>
  )
}
