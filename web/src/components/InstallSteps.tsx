// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Anchor, Badge, Group, Stack, Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { components } from '../api/schema'
import { RELEASE_KEY_DOC, stepText, type InstallStep } from '../install'
import { tones } from '../theme'
import { CopyBox } from './CopyBox'
import { Mono } from './Mono'

type ReleaseKey = components['schemas']['ReleaseKey']

// The key the packages are signed with, to compare with the README outside the server: a key that
// came from the server proves nothing about the server.
function SignatureKey({ releaseKey }: { releaseKey: ReleaseKey }) {
  const { t } = useTranslation()
  return (
    <Stack gap={4}>
      <Text size="sm">
        {t('install.keyId')}: <Mono>{releaseKey.id}</Mono>
      </Text>
      <Text size="sm">
        {t('install.keyString')}:{' '}
        <Mono style={{ wordBreak: 'break-all' }}>{releaseKey.publicKey}</Mono>
      </Text>
      <Text size="sm">
        {t('install.keyCheck')}{' '}
        <Anchor href={RELEASE_KEY_DOC} target="_blank" rel="noopener noreferrer">
          {t('install.keyDoc')}
        </Anchor>
      </Text>
      <Text size="sm">{t('install.keyNeeds')}</Text>
    </Stack>
  )
}

function Step({
  step,
  number,
  releaseKey,
}: {
  step: InstallStep
  number: number
  releaseKey: ReleaseKey
}) {
  const { t } = useTranslation()
  return (
    <Stack gap={4}>
      <Group gap="xs">
        <Text fw={600}>
          {number}. {t(`install.steps.${step.kind}.title`)}
        </Text>
        {step.optional && <Badge variant="light">{t('install.optional')}</Badge>}
      </Group>
      <Text size="sm" c="dimmed">
        {t(`install.steps.${step.kind}.text`)}
      </Text>
      {step.kind === 'signature' && <SignatureKey releaseKey={releaseKey} />}
      <CopyBox value={stepText(step)} label={t('install.commands')} />
    </Stack>
  )
}

// The steps in the server's order, each with its own copy button. A release without a signature
// says so where the signature step would be.
export function InstallSteps({
  steps,
  signed,
  releaseKey,
}: {
  steps: InstallStep[]
  signed: boolean
  releaseKey: ReleaseKey
}) {
  const { t } = useTranslation()
  return (
    <Stack>
      {steps.map((step, index) => (
        <Stack key={step.kind} gap="sm">
          <Step step={step} number={index + 1} releaseKey={releaseKey} />
          {step.kind === 'checksum' && !signed && (
            <Alert color={tones.notice}>{t('install.unsigned')}</Alert>
          )}
        </Stack>
      ))}
    </Stack>
  )
}
