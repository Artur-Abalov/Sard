// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { FieldFailure } from './errors'
import { mapField, type FieldTarget } from './fieldPath'

export type ErrorText = FieldFailure['text']

// The key of a form place: "name", "agent", "config", "config:paths", "config:paths/0", "form", ...
export function targetKey(target: FieldTarget): string {
  return target.kind === 'configField' ? `config:${target.path.join('/')}` : target.kind
}

// The errors of a 422 grouped by the place in the form they belong to, so that all of them show
// at once, each at its own field. `configFields` are the properties the config form has.
export function groupFieldErrors(
  failures: FieldFailure[],
  configFields: readonly string[],
): Record<string, ErrorText[]> {
  const grouped: Record<string, ErrorText[]> = {}
  for (const failure of failures) {
    const key = targetKey(mapField(failure.field, configFields))
    grouped[key] = [...(grouped[key] ?? []), failure.text]
  }
  return grouped
}
