// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Tooltip } from '@mantine/core'
import { EMPTY } from '../format'
import { useFormat } from '../useFormat'
import { Mono } from './Mono'

// A time as how long ago it was ("3 h ago"); the exact time is in the tooltip.
export function Time({ value }: { value: string | null }) {
  const format = useFormat()
  if (value === null) {
    return <Mono>{EMPTY}</Mono>
  }
  return (
    <Tooltip label={format.time(value)}>
      <Mono>{format.relative(value)}</Mono>
    </Tooltip>
  )
}
