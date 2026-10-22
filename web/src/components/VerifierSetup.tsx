// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Card, Stack, Text, Title } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { components } from '../api/schema'
import { CopyBox } from './CopyBox'
import { Mono } from './Mono'

type Entry = components['schemas']['VerifierSetupEntry']

function Entries({ entry }: { entry: Entry }) {
  const { t } = useTranslation()
  if (entry.keyAddCommand === null) return <Text c="dimmed">{t('verifierSetup.unsafeName')}</Text>
  return (
    <>
      <CopyBox value={entry.keyAddCommand} label={t('verifierSetup.keyAddHint')} />
      {entry.repoAddTemplate !== null && (
        <CopyBox value={entry.repoAddTemplate} label={t('verifierSetup.repoAddHint')} />
      )}
    </>
  )
}

// The key of a verifier (V3, КВ5): per repository its sources hold and it does not, the command for the source
// host and the template of the command for this verifier. The server builds both; no password is shown.
export function VerifierSetup({ entries }: { entries: Entry[] }) {
  const { t } = useTranslation()
  return (
    <Card withBorder>
      <Title order={4}>{t('verifierSetup.title')}</Title>
      <Stack mt="xs">
        {entries.length === 0 && <Text c="dimmed">{t('verifierSetup.empty')}</Text>}
        {entries.map((entry) => (
          <Stack key={entry.repositoryId} gap="xs">
            <Text>
              {t('verifierSetup.source')}: <Mono>{entry.sourceHostname}</Mono>{' '}
              <Mono>{entry.repositoryName}</Mono>
            </Text>
            <Entries entry={entry} />
          </Stack>
        ))}
      </Stack>
    </Card>
  )
}
