// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { DEFAULT_VERSION, stampVersion, versionOf } from './buildVersion'

describe('versionOf', () => {
  test('the build version is SARD_VERSION', () => {
    expect(versionOf('0.0.0-qa')).toBe('0.0.0-qa')
  })

  test('without SARD_VERSION, or with a blank one, the version is dev', () => {
    expect(DEFAULT_VERSION).toBe('dev')
    expect(versionOf(undefined)).toBe('dev')
    expect(versionOf('')).toBe('dev')
    expect(versionOf('  ')).toBe('dev')
  })

  test('surrounding whitespace is dropped', () => {
    expect(versionOf(' 1.2.3\n')).toBe('1.2.3')
  })
})

describe('stampVersion', () => {
  const page = '<meta name="sard-version" content="%SARD_VERSION%" />'

  test('the placeholder in the page becomes the version', () => {
    expect(stampVersion(page, '1.2.3')).toBe('<meta name="sard-version" content="1.2.3" />')
  })

  test('a version cannot break out of the attribute', () => {
    expect(stampVersion(page, 'a"><script>&')).toBe(
      '<meta name="sard-version" content="a&quot;&gt;&lt;script&gt;&amp;" />',
    )
  })

  test('every placeholder is replaced', () => {
    expect(stampVersion('%SARD_VERSION% %SARD_VERSION%', '1')).toBe('1 1')
  })

  test('a version holding a replacement pattern is taken literally', () => {
    expect(stampVersion(page, '$&')).toBe('<meta name="sard-version" content="$&amp;" />')
  })
})
