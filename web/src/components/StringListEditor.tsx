// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { ActionIcon, Button, Group, Stack, Text, TextInput } from '@mantine/core'
import { useTranslation } from 'react-i18next'

// An array of strings edited by adding and removing elements; an error may belong to the whole
// list or to one element (its index).
export function StringListEditor({
  label,
  description,
  placeholder,
  required,
  values,
  onChange,
  error,
  elementErrors,
}: {
  label: string
  description: string | null
  placeholder: string | null
  required: boolean
  values: string[]
  onChange: (values: string[]) => void
  error?: string
  elementErrors: Record<number, string>
}) {
  const { t } = useTranslation()
  return (
    <Stack gap="xs" role="group" aria-label={label}>
      <Text size="sm" fw={500}>
        {label}
        {required && ' *'}
      </Text>
      {description && (
        <Text size="xs" c="dimmed">
          {description}
        </Text>
      )}
      {values.map((value, index) => (
        <Group key={index} wrap="nowrap" align="flex-start">
          <TextInput
            style={{ flex: 1 }}
            aria-label={`${label} ${index + 1}`}
            placeholder={placeholder ?? undefined}
            value={value}
            error={elementErrors[index]}
            onChange={(event) =>
              onChange(values.map((v, i) => (i === index ? event.currentTarget.value : v)))
            }
          />
          <ActionIcon
            variant="default"
            size="lg"
            aria-label={t('form.remove')}
            onClick={() => onChange(values.filter((_, i) => i !== index))}
          >
            −
          </ActionIcon>
        </Group>
      ))}
      {error && (
        <Text size="xs" c="red" role="alert">
          {error}
        </Text>
      )}
      <Button
        variant="default"
        size="xs"
        style={{ alignSelf: 'flex-start' }}
        onClick={() => onChange([...values, ''])}
      >
        {t('form.add')}
      </Button>
    </Stack>
  )
}
