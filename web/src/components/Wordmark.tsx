// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Group, Text } from '@mantine/core'
import { shellBrand } from '../brand'
import { wordmark } from '../theme'
import { LogoMark } from './LogoMark'

// The mark and "sard" in lowercase: the only place the brand font is used.
export function Wordmark() {
  return (
    <Group gap="xs" wrap="nowrap">
      <LogoMark />
      <Text span style={wordmark}>
        {shellBrand()}
      </Text>
    </Group>
  )
}
