// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'
import { dictionary, EMPTY, fill, formatBytes, formatCount, formatDuration } from './format'
import type { tones } from './theme'

type Schemas = components['schemas']

// Restore verification in words (V6): the server decides every outcome, level, kind and reason; the console only
// puts the codes it receives into the language of the interface. Pure functions over the locale files, like format.ts.

type Role = keyof typeof tones

const MINUTE_MS = 60_000
const HOUR_MS = 3_600_000
const DAY_MS = 86_400_000

// The level a verification reached, in words (V-D2); a level the console does not know is its number.
export function levelWords(level: number, language: string): string {
  const levels: Record<string, string> = dictionary(language).verification.levels
  return levels[String(level)] ?? String(level)
}

type AgoUnit = 'minute' | 'hour' | 'day'

// "5 hours", "an hour": the largest unit that fits, rounded down; the exact word stands for exactly one.
function span(milliseconds: number): { unit: AgoUnit; count: number } {
  if (milliseconds >= DAY_MS) return { unit: 'day', count: Math.floor(milliseconds / DAY_MS) }
  if (milliseconds >= HOUR_MS) return { unit: 'hour', count: Math.floor(milliseconds / HOUR_MS) }
  return { unit: 'minute', count: Math.floor(milliseconds / MINUTE_MS) }
}

// "5 hours ago", "an hour ago", "just now": how long ago in the words of the language.
function agoWords(value: string, now: number, language: string): string {
  const { ago } = dictionary(language).verification
  const milliseconds = Math.max(0, now - Date.parse(value))
  if (milliseconds < MINUTE_MS) return ago.now
  const { unit, count } = span(milliseconds)
  const text =
    count === 1
      ? ago.exact[unit]
      : fill(
          ago[unit][new Intl.PluralRules(language).select(count) as keyof (typeof ago)[AgoUnit]],
          {
            count: formatCount(count, language),
          },
        )
  return fill(ago.suffix, { value: text })
}

export interface MetricFields {
  enabled: boolean
  lastVerifiedAt: string | null
  lastVerifiedLevel: number | null
}

// The main metric of a source (rule 1): "verified 5 hours ago — restore time measured"; a source never verified
// says so instead of staying empty, and one with verification off says that too.
export function verificationMetric(fields: MetricFields, now: number, language: string): string {
  const { metric } = dictionary(language).verification
  const { enabled, lastVerifiedAt, lastVerifiedLevel } = fields
  if (lastVerifiedAt === null) return enabled ? metric.never : metric.off
  const when = agoWords(lastVerifiedAt, now, language)
  if (!enabled) return fill(metric.offLast, { when })
  if (lastVerifiedLevel === null) return fill(metric.verified, { when })
  return fill(metric.verifiedLevel, { when, level: levelWords(lastVerifiedLevel, language) })
}

export interface OutcomeView {
  label: string
  // A role of the theme (tones): a failed backup is an error, a check that did not take place is only a warning.
  role: Role
}

const OUTCOME_ROLES: Record<Schemas['VerificationOutcome'], Role> = {
  passed: 'success',
  failed: 'error',
  cannot_verify: 'warning',
  infrastructure_failure: 'warning',
  no_verifier: 'warning',
  // A person stopped it: not bad news, so neither the error nor the warning color (J1f, ВJf2).
  cancelled: 'neutral',
}

// The two kinds of bad news never merge into one red badge (V-D10).
export function outcomeView(status: Schemas['VerificationOutcome'], language: string): OutcomeView {
  return { label: dictionary(language).verification.outcome[status], role: OUTCOME_ROLES[status] }
}

// The cancel button of the tab is for a job nobody asked to cancel yet: the server marks a job that is being cancelled
// (J1f, ВJf1); the console only reads the mark.
export function jobCancellable(job: Pick<Schemas['VerificationJob'], 'cancellation'>): boolean {
  return job.cancellation === null
}

// The refusal of a cancel of a job that ended says «the verification», not «the run»: the code is the run's.
export function jobCancelMessageKey(key: string): string {
  return key === 'errors.run_finished' ? 'verification.tab.cancel.finished' : key
}

export function verifierReasonText(reason: Schemas['VerifierReason'], language: string): string {
  return dictionary(language).verification.reason[reason]
}

// A stage of the pipeline by its name; an unknown name is shown as it is.
export function stageLabel(name: string, language: string): string {
  const stages: Record<string, string> = dictionary(language).verification.stage
  return stages[name] ?? name
}

// What to do when a stage could not run (rule 5): the server sends no structured reason, so the hint is by stage.
export function stageHint(name: string, language: string): string | null {
  const hints: Record<string, string> = dictionary(language).verification.stageHint
  return hints[name] ?? null
}

// A count of the fact as it came from the server; anything that is no whole number stays as it is.
function counted(value: string, language: string): string {
  return /^\d+$/.test(value) ? formatCount(Number(value), language) : value
}

function sized(value: string, language: string): string {
  return /^\d+$/.test(value) ? formatBytes(Number(value), language) : value
}

type MismatchSentence = (mismatch: Schemas['FingerprintMismatch'], language: string) => string

function generic(mismatch: Schemas['FingerprintMismatch'], language: string): string {
  const { mismatch: words } = dictionary(language).verification
  const { expected, actual } = mismatch
  return fill(words.other, { name: mismatch.name, expected, actual })
}

function ofCounts(key: 'rows' | 'tables' | 'files'): MismatchSentence {
  return (mismatch, language) => {
    const { mismatch: words } = dictionary(language).verification
    return fill(words[key], {
      subject: mismatch.subject,
      expected: counted(mismatch.expected, language),
      actual: counted(mismatch.actual, language),
    })
  }
}

function ofBytes(mismatch: Schemas['FingerprintMismatch'], language: string): string {
  const { mismatch: words } = dictionary(language).verification
  return fill(words.bytes, {
    expected: sized(mismatch.expected, language),
    actual: sized(mismatch.actual, language),
  })
}

// Every kind the server names has its place here; the ones without a sentence of their own are said as the generic.
const SENTENCES: Record<Schemas['FingerprintMismatchKind'], MismatchSentence> = {
  rows: ofCounts('rows'),
  tables: ofCounts('tables'),
  files: ofCounts('files'),
  bytes: ofBytes,
  control_path: (mismatch, language) => controlPath(mismatch, language),
  extension: generic,
  extension_version: generic,
  file_hash: generic,
  other: generic,
}

// A mismatch said in words (rule 4): the server gives the kind and the subject, the console the sentence.
export function mismatchText(mismatch: Schemas['FingerprintMismatch'], language: string): string {
  return SENTENCES[mismatch.kind](mismatch, language)
}

function controlPath(mismatch: Schemas['FingerprintMismatch'], language: string): string {
  const { mismatch: words } = dictionary(language).verification
  const { subject, actual } = mismatch
  if (actual === 'empty') return fill(words.controlEmpty, { subject })
  if (actual === 'missing') return fill(words.controlMissing, { subject })
  return fill(words.other, { name: mismatch.name, expected: mismatch.expected, actual })
}

// The time to restore the server measured (level 5), as a duration; nothing measured is EMPTY.
export function restoreTimeText(milliseconds: number | null, language: string): string {
  if (milliseconds === null) return EMPTY
  const epoch = new Date(0).toISOString()
  return formatDuration(epoch, new Date(milliseconds).toISOString(), 0, language)
}
