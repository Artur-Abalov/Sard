// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { dictionary, fill } from './format'
import { productName } from './product'

// The title of the report on the verification of a source (rule 20): the product's name comes from product.ts.
export function reportTitle(sourceName: string, language: string): string {
  return fill(dictionary(language).report.title, { product: productName, source: sourceName })
}

// The period of a report in the days of the cycle's zone: the end is excluded, so the last day is the one before it.
export function reportPeriodText(
  from: string,
  to: string,
  timeZone: string,
  language: string,
): string {
  const day = (value: number) =>
    new Date(value).toLocaleDateString(language, { dateStyle: 'medium', timeZone })
  return fill(dictionary(language).report.period, {
    from: day(Date.parse(from)),
    to: day(Date.parse(to) - 1),
    zone: timeZone,
  })
}
