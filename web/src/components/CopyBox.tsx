// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Button, Code, Group, Stack, Text } from '@mantine/core'
import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { tones } from '../theme'

// A value to copy, with its own copy button. Where the clipboard is not available (a page
// opened over plain HTTP) it says so and selects the whole value for copying by hand.
export function CopyBox({
  value,
  label,
  unbroken = false,
}: {
  value: string
  label: string
  // Keep the value on one line (a fingerprint is compared by eye), scrolling instead of wrapping.
  unbroken?: boolean
}) {
  const { t } = useTranslation()
  const text = useRef<HTMLElement>(null)
  const [state, setState] = useState<'idle' | 'copied' | 'refused'>('idle')

  async function copy() {
    try {
      await navigator.clipboard.writeText(value)
      setState('copied')
    } catch {
      setState('refused')
      if (text.current) window.getSelection()?.selectAllChildren(text.current)
    }
  }

  return (
    <Stack gap={4}>
      <Text size="sm" fw={500}>
        {label}
      </Text>
      <Group align="flex-start" wrap="nowrap">
        <Code
          ref={text}
          block
          style={
            unbroken
              ? { flex: 1, whiteSpace: 'nowrap', overflowX: 'auto' }
              : { flex: 1, wordBreak: 'break-all' }
          }
        >
          {value}
        </Code>
        <Button variant="default" size="xs" onClick={() => void copy()}>
          {t('common.copy')}
        </Button>
      </Group>
      {state === 'copied' && (
        <Text size="xs" role="status">
          {t('common.copied')}
        </Text>
      )}
      {state === 'refused' && (
        <Text size="xs" c={tones.error} role="alert">
          {t('common.copyFailed')}
        </Text>
      )}
    </Stack>
  )
}
