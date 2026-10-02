// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Text, type TextProps } from '@mantine/core'
import type { PropsWithChildren } from 'react'
import { tabularNums } from '../theme'

// What a user may copy or compare - hostnames, names, paths, hashes, sizes, durations, times -
// in the monospace font, its digits tabular.
export function Mono({ children, ...props }: PropsWithChildren<TextProps>) {
  return (
    <Text span ff="monospace" style={tabularNums} {...props}>
      {children}
    </Text>
  )
}
