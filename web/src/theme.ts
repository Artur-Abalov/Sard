// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import {
  Anchor,
  Badge,
  Button,
  Card,
  Code,
  createTheme,
  Table,
  Tooltip,
  type CSSVariablesResolver,
  type MantineColorScheme,
  type MantineColorsTuple,
} from '@mantine/core'
import type { CSSProperties } from 'react'

// The single place for visual design (docs/design/DESIGN.md, ADR 0035): the tokens, the
// Mantine theme built on them, the CSS variables both color schemes receive, and the few
// style objects components share. Components name a tone or a status, never a color.

type Tokens = Record<string, string>

const shared: Tokens = {
  // Brand palette
  '--sard-graphite-950': '#0B0C0E',
  '--sard-graphite-900': '#121416',
  '--sard-graphite-850': '#1A1D20',
  '--sard-graphite-700': '#262A2E',
  '--sard-graphite-600': '#3A3F44',
  '--sard-graphite-400': '#8C8983',
  '--sard-silk-200': '#B9B6AE',
  '--sard-silk-50': '#ECEAE4',
  '--sard-amber-500': '#E8B04B',
  '--sard-amber-700': '#B5791A',
  // Type. Unbounded is the brand font and is not bundled yet: the stack falls back to the system fonts.
  '--sard-font-brand': '"Unbounded", "Segoe UI", system-ui, sans-serif',
  '--sard-font-ui': '"IBM Plex Sans", "Segoe UI", system-ui, sans-serif',
  '--sard-font-mono': '"JetBrains Mono", ui-monospace, "SFMono-Regular", Menlo, monospace',
  '--sard-text-xs': '12px',
  '--sard-text-sm': '13px',
  '--sard-text-md': '14px',
  '--sard-text-lg': '16px',
  '--sard-text-xl': '20px',
  '--sard-text-2xl': '26px',
  '--sard-text-metric': '40px',
  '--sard-leading-tight': '1.25',
  '--sard-leading-ui': '1.45',
  // Spacing, a 4px step
  '--sard-space-1': '4px',
  '--sard-space-2': '8px',
  '--sard-space-3': '12px',
  '--sard-space-4': '16px',
  '--sard-space-5': '24px',
  '--sard-space-6': '32px',
  '--sard-space-7': '48px',
  // Radii by hierarchy
  '--sard-radius-sm': '4px',
  '--sard-radius-md': '8px',
  '--sard-radius-lg': '12px',
  '--sard-radius-pill': '999px',
  '--sard-duration': '150ms',
}

const dark: Tokens = {
  '--sard-status-verified': '#5FBF8A',
  '--sard-status-ok': '#7FA6D9',
  '--sard-status-stale': '#E0893A',
  '--sard-status-failed': '#E5604E',
  '--sard-status-paused': '#8C8983',
  '--sard-bg': 'var(--sard-graphite-900)',
  '--sard-bg-sunken': 'var(--sard-graphite-950)',
  '--sard-surface': 'var(--sard-graphite-850)',
  '--sard-border': 'var(--sard-graphite-700)',
  '--sard-border-strong': 'var(--sard-graphite-600)',
  '--sard-text': 'var(--sard-silk-50)',
  '--sard-text-muted': 'var(--sard-silk-200)',
  '--sard-text-subtle': 'var(--sard-graphite-400)',
  '--sard-accent': 'var(--sard-amber-500)',
  '--sard-on-accent': 'var(--sard-graphite-900)',
  '--sard-focus': 'var(--sard-amber-500)',
  '--sard-shadow-pop': '0 8px 24px rgba(0, 0, 0, 0.35)',
}

const light: Tokens = {
  '--sard-bg': '#F3F4F5',
  '--sard-bg-sunken': '#E8EAEC',
  '--sard-surface': '#FFFFFF',
  '--sard-border': '#D9DCDF',
  '--sard-border-strong': '#BFC4C9',
  '--sard-text': '#14171A',
  '--sard-text-muted': '#454B52',
  '--sard-text-subtle': '#6B7178',
  '--sard-accent': 'var(--sard-amber-700)',
  '--sard-on-accent': '#FFFFFF',
  '--sard-focus': 'var(--sard-amber-700)',
  '--sard-status-verified': '#23854F',
  '--sard-status-ok': '#3567A8',
  '--sard-status-stale': '#B05E14',
  '--sard-status-failed': '#C23A28',
  '--sard-status-paused': '#6B7178',
  '--sard-shadow-pop': '0 8px 24px rgba(20, 23, 26, 0.14)',
}

// Equal to docs/design/tokens.css (a test keeps them so): the shared tokens and the dark roles
// together are its first block, the light roles are its light block.
const reducedMotion: Tokens = { '--sard-duration': '0ms' }

export const tokens = { shared, dark, light, reducedMotion }

// The reduced-motion override of tokens.css: Mantine's resolver has no media queries, so
// main.tsx puts this in a <style>. ":root:root" outranks the variables the resolver emits.
export const reducedMotionCss = `@media (prefers-reduced-motion: reduce) { :root:root { ${Object.entries(
  reducedMotion,
)
  .map(([name, value]) => `${name}: ${value}`)
  .join('; ')} } }`

// The node of the logo mark is the accent of the scheme.
export const logoNodeFill = 'var(--sard-accent)'

// "--sard-text-md" -> its value, for the places that need a plain value (Mantine's scales).
function scale(prefix: string): Record<string, string> {
  return Object.fromEntries(
    Object.entries(shared)
      .filter(([name]) => name.startsWith(prefix))
      .map(([name, value]) => [name.slice(prefix.length), value]),
  )
}

const textSizes = scale('--sard-text-')
const spaces = scale('--sard-space-')
const radii = scale('--sard-radius-')

// Status colors: Mantine colors of one value each, so a component can say color="ok". The
// shades are placeholders for Mantine's own arithmetic; the variables below carry the real look.
function flat(value: string): MantineColorsTuple {
  return Array.from({ length: 10 }, () => value) as unknown as MantineColorsTuple
}

// Mantine's neutral palettes, made of the tokens: `dark` is what Mantine reads in the dark
// scheme, `gray` in the light one.
const darkPalette = [
  '#ECEAE4',
  '#B9B6AE',
  '#8C8983',
  '#8C8983',
  '#262A2E',
  '#262A2E',
  '#1A1D20',
  '#121416',
  '#0B0C0E',
  '#0B0C0E',
] as unknown as MantineColorsTuple
const grayPalette = [
  '#F3F4F5',
  '#E8EAEC',
  '#D9DCDF',
  '#D9DCDF',
  '#BFC4C9',
  '#6B7178',
  '#6B7178',
  '#454B52',
  '#454B52',
  '#14171A',
] as unknown as MantineColorsTuple

const STATUS_COLORS = ['verified', 'ok', 'stale', 'failed', 'paused'] as const

// A status or the accent as a Mantine color: text and fills in the role's color, tints at 14%.
function colorVariables(name: string, role: string): Tokens {
  const color = `var(${role})`
  const tint = (percent: number) => `color-mix(in srgb, ${color} ${percent}%, transparent)`
  return {
    [`--mantine-color-${name}-text`]: color,
    [`--mantine-color-${name}-filled`]: color,
    [`--mantine-color-${name}-filled-hover`]: `color-mix(in srgb, ${color} 88%, var(--sard-text))`,
    [`--mantine-color-${name}-light`]: tint(14),
    [`--mantine-color-${name}-light-hover`]: tint(20),
    [`--mantine-color-${name}-light-color`]: color,
    [`--mantine-color-${name}-outline`]: color,
    [`--mantine-color-${name}-outline-hover`]: tint(8),
  }
}

// What Mantine reads for its page, text and borders: the roles, whichever scheme is on.
const mantineRoles: Tokens = {
  '--mantine-color-body': 'var(--sard-bg)',
  '--mantine-color-text': 'var(--sard-text)',
  '--mantine-color-bright': 'var(--sard-text)',
  '--mantine-color-dimmed': 'var(--sard-text-muted)',
  '--mantine-color-placeholder': 'var(--sard-text-subtle)',
  '--mantine-color-anchor': 'var(--sard-text)',
  '--mantine-color-default': 'var(--sard-surface)',
  '--mantine-color-default-hover': 'var(--sard-bg-sunken)',
  '--mantine-color-default-color': 'var(--sard-text)',
  '--mantine-color-default-border': 'var(--sard-border-strong)',
  '--mantine-color-error': 'var(--sard-status-failed)',
  ...colorVariables('accent', '--sard-accent'),
  ...Object.assign({}, ...STATUS_COLORS.map((s) => colorVariables(s, `--sard-status-${s}`))),
}

// Emits the --sard-* tokens: the shared ones everywhere, the roles per color scheme, and
// points Mantine's own variables at them.
export const cssVariablesResolver: CSSVariablesResolver = () => ({
  variables: { ...shared },
  light: { ...light, ...mantineRoles },
  dark: { ...dark, ...mantineRoles },
})

const noShadow = 'none'
const popShadow = 'var(--sard-shadow-pop)'

export const theme = createTheme({
  primaryColor: 'accent',
  defaultRadius: 'md',
  respectReducedMotion: true,
  focusRing: 'auto',
  fontFamily: shared['--sard-font-ui'],
  fontFamilyMonospace: shared['--sard-font-mono'],
  fontSizes: {
    xs: textSizes.xs,
    sm: textSizes.sm,
    md: textSizes.md,
    lg: textSizes.lg,
    xl: textSizes.xl,
  },
  lineHeights: {
    xs: shared['--sard-leading-ui'],
    sm: shared['--sard-leading-ui'],
    md: shared['--sard-leading-ui'],
    lg: shared['--sard-leading-ui'],
    xl: shared['--sard-leading-tight'],
  },
  spacing: { xs: spaces['2'], sm: spaces['3'], md: spaces['4'], lg: spaces['5'], xl: spaces['6'] },
  radius: { xs: '2px', sm: radii.sm, md: radii.md, lg: radii.lg, xl: radii.pill },
  // Panels are told apart by a border, not a shadow: only what pops up casts one.
  shadows: {
    xs: noShadow,
    sm: noShadow,
    md: popShadow,
    lg: popShadow,
    xl: popShadow,
  },
  headings: {
    fontFamily: shared['--sard-font-ui'],
    fontWeight: '600',
    sizes: {
      h1: { fontSize: textSizes['2xl'], lineHeight: shared['--sard-leading-tight'] },
      h2: { fontSize: textSizes.xl, lineHeight: shared['--sard-leading-tight'] },
      h3: { fontSize: textSizes.lg, lineHeight: shared['--sard-leading-tight'] },
      h4: { fontSize: textSizes.md, lineHeight: shared['--sard-leading-ui'] },
      h5: { fontSize: textSizes.sm, lineHeight: shared['--sard-leading-ui'] },
      h6: { fontSize: textSizes.xs, lineHeight: shared['--sard-leading-ui'] },
    },
  },
  colors: {
    dark: darkPalette,
    gray: grayPalette,
    accent: flat(shared['--sard-amber-500']),
    verified: flat(dark['--sard-status-verified']),
    ok: flat(dark['--sard-status-ok']),
    stale: flat(dark['--sard-status-stale']),
    failed: flat(dark['--sard-status-failed']),
    paused: flat(dark['--sard-status-paused']),
  },
  primaryShade: 6,
  components: {
    Anchor: Anchor.extend({ defaultProps: { underline: 'hover' } }),
    Badge: Badge.extend({
      defaultProps: { variant: 'light', radius: 'sm' },
      // Sentence case, never clipped: the label is as wide as its text.
      styles: {
        root: {
          height: 'auto',
          minHeight: 22,
          textTransform: 'none',
          fontWeight: 400,
          fontSize: 'var(--sard-text-sm)',
          overflow: 'visible',
        },
        label: { overflow: 'visible', textOverflow: 'clip' },
      },
    }),
    Button: Button.extend({
      defaultProps: { radius: 'md' },
      vars: (_theme, props) => ({
        root: {
          '--button-color':
            props.variant === undefined || props.variant === 'filled'
              ? 'var(--sard-on-accent)'
              : undefined,
        },
      }),
      styles: { root: { fontWeight: 600, transitionDuration: 'var(--sard-duration)' } },
    }),
    Card: Card.extend({
      defaultProps: { radius: 'lg', withBorder: true, shadow: undefined },
      styles: {
        root: { background: 'var(--sard-surface)', '--paper-border-color': 'var(--sard-border)' },
      },
    }),
    Code: Code.extend({
      styles: {
        root: {
          background: 'var(--sard-bg-sunken)',
          fontFamily: 'var(--sard-font-mono)',
          fontSize: 'var(--sard-text-sm)',
        },
      },
    }),
    Table: Table.extend({
      defaultProps: {
        highlightOnHover: true,
        highlightOnHoverColor: 'var(--sard-bg-sunken)',
        borderColor: 'var(--sard-border)',
        verticalSpacing: 'xs',
      },
      // 40px rows, sentence-case headers, no zebra.
      styles: {
        tr: { height: 40 },
        th: { fontWeight: 600, textTransform: 'none', color: 'var(--sard-text-muted)' },
      },
    }),
    Tooltip: Tooltip.extend({ defaultProps: { openDelay: 200 } }),
  },
})

// "auto" follows the operating system until the user picks a scheme: dark, or light when
// the system asks for it (docs/design/DESIGN.md, "Цвет").
export const defaultColorScheme: MantineColorScheme = 'auto'

// Content is left-aligned and never wider than this.
export const contentMaxWidth = 1440

// Figures in tables line up by digit.
export const tabularNums: CSSProperties = { fontVariantNumeric: 'tabular-nums' }

// The wordmark: lowercase, in the brand font (system fallback until Unbounded is bundled).
export const wordmark: CSSProperties = {
  fontFamily: 'var(--sard-font-brand)',
  fontWeight: 700,
  fontSize: 'var(--sard-text-xl)',
  letterSpacing: 0,
  textTransform: 'none',
  color: 'var(--sard-text)',
}

// Logs and code: a sunken block that scrolls inside itself, so a long log never stretches the page.
export const codeBlock: CSSProperties = {
  background: 'var(--sard-bg-sunken)',
  fontFamily: 'var(--sard-font-mono)',
  fontSize: 'var(--sard-text-sm)',
  borderRadius: 'var(--sard-radius-md)',
  border: '1px solid var(--sard-border)',
  maxHeight: 480,
  overflow: 'auto',
}

// Names of the drawings StatusIcon has; one per meaning, so a status is told by its icon too.
export type IconName =
  | 'check'
  | 'x-circle'
  | 'x-square'
  | 'clock'
  | 'clock-dashed'
  | 'hourglass'
  | 'arrow'
  | 'play'
  | 'stop'
  | 'dot'
  | 'circle'
  | 'minus'

export interface StatusVisual {
  // A color of the theme; a status is told by its icon and label too, never by color alone.
  color: string
  icon: IconName
}

// How each status value looks: runs, steps, tokens and agents share the values that
// mean the same (queued, running, ...). The only place status colors and icons are chosen.
// Stage 1 has no restore verification, so nothing is verified (green): a success is
// "backup exists" (blue) and what is under way or was stopped is neutral.
export const statusVisuals: Record<string, StatusVisual> = {
  queued: { color: 'paused', icon: 'hourglass' },
  dispatched: { color: 'paused', icon: 'arrow' },
  running: { color: 'paused', icon: 'play' },
  succeeded: { color: 'ok', icon: 'check' },
  failed: { color: 'failed', icon: 'x-circle' },
  cancelled: { color: 'paused', icon: 'stop' },
  timed_out: { color: 'stale', icon: 'clock' },
  rejected: { color: 'failed', icon: 'x-square' },
  lost: { color: 'stale', icon: 'clock-dashed' },
  active: { color: 'ok', icon: 'dot' },
  used: { color: 'paused', icon: 'check' },
  expired: { color: 'stale', icon: 'clock' },
  revoked: { color: 'failed', icon: 'x-circle' },
  online: { color: 'ok', icon: 'dot' },
  offline: { color: 'paused', icon: 'circle' },
}

const UNKNOWN_STATUS: StatusVisual = { color: 'paused', icon: 'minus' }

export function statusVisual(value: string): StatusVisual {
  return Object.hasOwn(statusVisuals, value) ? statusVisuals[value] : UNKNOWN_STATUS
}

// Colors of log levels.
export const logLevelColors: Record<string, string> = {
  debug: 'paused',
  info: 'ok',
  warn: 'stale',
  error: 'failed',
}

// Semantic tones: components say what a message means, the theme says how it looks.
export const tones = {
  error: 'failed',
  warning: 'stale',
  notice: 'stale',
  info: 'ok',
  success: 'ok',
  neutral: 'paused',
} as const
