// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Text, Tooltip } from '@mantine/core'
import { useFormat } from '../useFormat'
import type { MetricFields } from '../verification'

// The main metric of a source: when its restore was last proven and how far. A source never verified says so;
// the exact time of the last pass is in the tooltip.
export function VerificationMetric(fields: MetricFields) {
  const format = useFormat()
  const text = format.verificationMetric(fields)
  if (fields.lastVerifiedAt === null) return <Text span>{text}</Text>
  return (
    <Tooltip label={format.time(fields.lastVerifiedAt)}>
      <Text span tabIndex={0}>
        {text}
      </Text>
    </Tooltip>
  )
}
