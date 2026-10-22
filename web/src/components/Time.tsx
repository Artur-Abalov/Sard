// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Text, Tooltip, VisuallyHidden } from '@mantine/core'
import { EMPTY } from '../format'
import { useFormat } from '../useFormat'
import { tabularNums } from '../theme'
import { Mono } from './Mono'

// A time as how long ago it was ("3 h ago"), in the interface font with tabular digits; the exact
// time is in the tooltip and, for screen readers, in visually hidden text (aria-label is not
// allowed on a span without a role).
export function Time({ value }: { value: string | null }) {
  const format = useFormat()
  if (value === null) {
    return <Mono>{EMPTY}</Mono>
  }
  const exact = format.time(value)
  return (
    <Tooltip label={exact}>
      <Text span style={tabularNums} tabIndex={0}>
        <time dateTime={value}>{format.relative(value)}</time>
        <VisuallyHidden>{exact}</VisuallyHidden>
      </Text>
    </Tooltip>
  )
}
