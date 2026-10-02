// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Stack, Text } from '@mantine/core'
import type { ReactNode } from 'react'

// What a list says when it has nothing to show, with the action that leads on.
export function EmptyState({ text, children }: { text: string; children?: ReactNode }) {
  return (
    <Stack align="flex-start" gap="xs" py="md">
      <Text c="dimmed">{text}</Text>
      {children}
    </Stack>
  )
}
