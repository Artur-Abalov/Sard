// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

/// <reference types="node" />
import { describe, expect, test } from 'vitest'
import spec from './api/openapi.json'
import { DEFAULT_THEME, mergeMantineTheme } from '@mantine/core'
import { readFileSync } from 'node:fs'
import {
  cssVariablesResolver,
  logoNodeFill,
  logLevelColors,
  reducedMotionCss,
  statusVisual,
  statusVisuals,
  theme,
  tokens,
  tones,
} from './theme'

function values(schema: string): string[] {
  const schemas = spec.components.schemas as unknown as Record<string, { enum: string[] }>
  return schemas[schema].enum
}

describe('status visuals', () => {
  test.each(['RunStatus', 'StepStatus', 'EnrollmentTokenStatus', 'AgentStatus'])(
    'every value of %s has a color and an icon',
    (schema) => {
      for (const value of values(schema)) {
        expect(Object.keys(statusVisuals), value).toContain(value)
        expect(statusVisual(value).icon, value).not.toBe(statusVisual('no-such-status').icon)
      }
    },
  )

  test.each(['RunStatus', 'StepStatus', 'EnrollmentTokenStatus', 'AgentStatus'])(
    'the values of %s differ by icon, not only by color',
    (schema) => {
      const icons = values(schema).map((value) => statusVisual(value).icon)

      expect(new Set(icons).size).toBe(icons.length)
    },
  )

  test('every log level has a color', () => {
    expect(Object.keys(logLevelColors).sort()).toEqual([...values('LogLevel')].sort())
  })

  test('a value nobody knows is shown plainly', () => {
    expect(statusVisual('constructor')).toEqual(statusVisual('no-such-status'))
  })
})

describe('status mapping of stage 1', () => {
  // Stage 1 has no restore verification: a success is "backup exists", blue, and nothing is green.
  test.each([
    ['succeeded', 'ok', 'check'],
    ['failed', 'failed', 'x-circle'],
    ['rejected', 'failed', 'x-square'],
    ['lost', 'stale', 'clock-dashed'],
    ['timed_out', 'stale', 'clock'],
    ['queued', 'paused', 'hourglass'],
    ['dispatched', 'paused', 'arrow'],
    ['running', 'paused', 'play'],
    ['cancelled', 'paused', 'stop'],
    ['active', 'ok', 'dot'],
    ['used', 'paused', 'check'],
    ['expired', 'stale', 'clock'],
    ['revoked', 'failed', 'x-circle'],
    ['online', 'ok', 'dot'],
    ['offline', 'paused', 'circle'],
  ])('%s is %s with the %s icon', (value, color, icon) => {
    expect(statusVisual(value)).toEqual({ color, icon })
  })

  test('nothing is shown as verified until restores are verified', () => {
    const colors = [
      ...Object.values(statusVisuals).map((visual) => visual.color),
      ...Object.values(logLevelColors),
      ...Object.values(tones),
    ]

    expect(colors).not.toContain('verified')
  })

  test('every color a visual or a tone names is a color of the theme', () => {
    const names = [
      ...Object.values(statusVisuals).map((visual) => visual.color),
      ...Object.values(logLevelColors),
      ...Object.values(tones),
    ]

    for (const name of names) expect(Object.keys(theme.colors ?? {}), name).toContain(name)
  })
})

// The tokens of docs/design/tokens.css, by role name, from the first (dark) or the light block.
function referenceTokens(block: 'dark' | 'light'): Record<string, string> {
  const css = readFileSync(
    new URL('../../docs/design/tokens.css', import.meta.url),
    'utf8',
  ).replace(/\/\*[\s\S]*?\*\//g, '')
  const start = block === 'dark' ? css.indexOf(':root {') : css.indexOf(':root[data-theme="light"]')
  const body = css.slice(css.indexOf('{', start) + 1, css.indexOf('}', start))
  return Object.fromEntries(
    [...body.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)].map(([, name, value]) => [
      name,
      value.replace(/\s+/g, ' ').trim(),
    ]),
  )
}

const full = mergeMantineTheme(DEFAULT_THEME, theme)

describe('design tokens', () => {
  test('the dark scheme and the shared tokens are those of docs/design/tokens.css', () => {
    expect({ ...tokens.shared, ...tokens.dark }).toEqual(referenceTokens('dark'))
  })

  test('the light scheme overrides exactly the roles of docs/design/tokens.css', () => {
    expect(tokens.light).toEqual(referenceTokens('light'))
  })

  test('the CSS variables of each scheme carry its roles, the shared ones go to both', () => {
    const resolved = cssVariablesResolver(full)

    expect(resolved.variables['--sard-radius-md']).toBe('8px')
    expect(resolved.dark['--sard-bg']).toBe('var(--sard-graphite-900)')
    expect(resolved.light['--sard-bg']).toBe(referenceTokens('light')['--sard-bg'])
    expect(resolved.light['--sard-status-ok']).toBe(referenceTokens('light')['--sard-status-ok'])
  })

  test('Mantine reads its page, text, border and accent from the roles', () => {
    const { dark } = cssVariablesResolver(full)

    expect(dark['--mantine-color-body']).toBe('var(--sard-bg)')
    expect(dark['--mantine-color-text']).toBe('var(--sard-text)')
    expect(dark['--mantine-color-default-border']).toBe('var(--sard-border-strong)')
    expect(dark['--mantine-color-accent-filled']).toBe('var(--sard-accent)')
    expect(dark['--mantine-color-failed-light']).toContain('var(--sard-status-failed)')
  })

  test('the accent is the primary color and is never a status color', () => {
    expect(theme.primaryColor).toBe('accent')
    expect(Object.values(statusVisuals).map((visual) => visual.color)).not.toContain('accent')
  })

  test('type, radii and spacing come from the tokens', () => {
    expect(theme.fontFamily).toBe(tokens.shared['--sard-font-ui'])
    expect(theme.fontFamilyMonospace).toBe(tokens.shared['--sard-font-mono'])
    expect(theme.fontSizes?.md).toBe('14px')
    expect(theme.fontSizes?.sm).toBe('13px')
    expect(theme.radius?.sm).toBe('4px')
    expect(theme.defaultRadius).toBe('md')
    expect(theme.spacing?.md).toBe('16px')
  })

  test('the interface font is not the brand font', () => {
    expect(tokens.shared['--sard-font-ui']).not.toContain('Unbounded')
    expect(tokens.shared['--sard-font-brand']).toMatch(/^"Unbounded"/)
  })

  test('only popovers cast a shadow, and motion respects the system setting', () => {
    expect(theme.shadows?.xs).toBe('none')
    expect(theme.shadows?.sm).toBe('none')
    expect(theme.shadows?.md).toBe('var(--sard-shadow-pop)')
    expect(theme.respectReducedMotion).toBe(true)
  })

  test('reduced motion zeroes the duration, as docs/design/tokens.css does', () => {
    const css = readFileSync(new URL('../../docs/design/tokens.css', import.meta.url), 'utf8')
    const block = css.slice(css.indexOf('@media (prefers-reduced-motion: reduce)'))

    expect(block).toMatch(/--sard-duration:\s*0ms/)
    expect(tokens.reducedMotion).toEqual({ '--sard-duration': '0ms' })
    expect(reducedMotionCss).toContain('@media (prefers-reduced-motion: reduce)')
    expect(reducedMotionCss).toContain('--sard-duration: 0ms')
  })

  test.each(['dark', 'light'] as const)(
    'the focus ring of the %s scheme is the accent, which Mantine draws it with',
    (scheme) => {
      const roles = scheme === 'dark' ? { ...tokens.shared, ...tokens.dark } : tokens.light

      expect(roles['--sard-focus']).toBe(roles['--sard-accent'])
    },
  )

  test('every color of the neutral palettes is a token value', () => {
    const values = new Set(
      [...Object.values(referenceTokens('dark')), ...Object.values(referenceTokens('light'))].map(
        (value) => value.toUpperCase(),
      ),
    )

    for (const color of [...(theme.colors?.dark ?? []), ...(theme.colors?.gray ?? [])]) {
      expect(values, color).toContain(color.toUpperCase())
    }
  })

  test('the logo node takes the accent of the scheme', () => {
    expect(logoNodeFill).toBe('var(--sard-accent)')
  })

  test('links are in the text color, the accent is kept for the primary button, focus and navigation', () => {
    const { dark, light } = cssVariablesResolver(full)

    expect(dark['--mantine-color-anchor']).toBe('var(--sard-text)')
    expect(light['--mantine-color-anchor']).toBe('var(--sard-text)')
    expect(full.components.Anchor.defaultProps.underline).toBe('hover')
  })

  test('a tooltip opens on keyboard focus too', () => {
    expect(full.components.Tooltip.defaultProps.events).toMatchObject({ hover: true, focus: true })
  })
})
