// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test, vi } from 'vitest'

// The name of the product has one place (D24: the report is a prototype of a paid MSP report): the header of the
// console and the title of the report both read it from there.
vi.mock('./product', () => ({ productName: 'QA-NAME' }))

describe('the name of the product', () => {
  test('the header and the title of the report take it from one place', async () => {
    const { shellBrand } = await import('./brand')
    const { reportTitle } = await import('./report')

    expect(shellBrand()).toBe('QA-NAME')
    expect(reportTitle('db-main', 'ru')).toBe('QA-NAME — отчёт о проверке восстановления: db-main')
    expect(reportTitle('db-main', 'en')).toBe('QA-NAME — restore verification report: db-main')
  })
})
