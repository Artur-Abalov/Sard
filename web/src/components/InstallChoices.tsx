// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Group, SegmentedControl, Stack, Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { ARCHES, FETCHES, FORMATS, type InstallChoice } from '../install'

function Choice<T extends string>({
  label,
  group,
  values,
  value,
  onChange,
}: {
  label: string
  group: 'arch' | 'format' | 'fetch'
  values: T[]
  value: T
  onChange: (value: T) => void
}) {
  const { t } = useTranslation()
  return (
    <Stack gap={4}>
      <Text size="sm" fw={500}>
        {label}
      </Text>
      <SegmentedControl
        size="xs"
        value={value}
        onChange={(next) => onChange(next as T)}
        data={values.map((item) => ({ value: item, label: t(`install.${group}.${item}`) }))}
      />
    </Stack>
  )
}

// What an administrator picks before the commands: architecture (not for an upgrade, where the
// agent's own is used), package format and the downloader. The server answers for the choice.
export function InstallChoices({
  choice,
  onChange,
  withArch,
}: {
  choice: InstallChoice
  onChange: (choice: InstallChoice) => void
  withArch: boolean
}) {
  const { t } = useTranslation()
  return (
    <Group align="flex-start">
      {withArch && (
        <Choice
          label={t('install.archLabel')}
          group="arch"
          values={ARCHES}
          value={choice.arch}
          onChange={(arch) => onChange({ ...choice, arch })}
        />
      )}
      <Choice
        label={t('install.formatLabel')}
        group="format"
        values={FORMATS}
        value={choice.format}
        onChange={(format) => onChange({ ...choice, format })}
      />
      <Choice
        label={t('install.fetchLabel')}
        group="fetch"
        values={FETCHES}
        value={choice.fetch}
        onChange={(fetch) => onChange({ ...choice, fetch })}
      />
    </Group>
  )
}
