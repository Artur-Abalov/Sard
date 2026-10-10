// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { PROBLEM, problem } from './problems'

/** Failures before the lockout and its window (К4: matches the server). */
export const MAX_FAILURES = 5
const WINDOW_MS = 15 * 60_000

/** Failed attempts of one kind (sign-in, setup code) and the lock the fifth one sets. */
export interface Tracker {
  /** Timestamps (ms) of failures, pruned to the 15-minute window. */
  failures: number[]
  /** When the fifth failure locked the attempts (ms), or null when they are not locked. */
  lockedAt: number | null
}

export function freshTracker(): Tracker {
  return { failures: [], lockedAt: null }
}

/** Seconds until the lock lifts, rounded up; null when not locked (matches LoginAttemptTracker). */
export function retryAfterSeconds(tracker: Tracker, now: number): number | null {
  if (tracker.lockedAt === null) return null
  const unlockAt = tracker.lockedAt + WINDOW_MS
  if (now >= unlockAt) {
    Object.assign(tracker, freshTracker())
    return null
  }
  return Math.max(Math.ceil((unlockAt - now) / 1000), 1)
}

export function recordFailure(tracker: Tracker, now: number): void {
  tracker.failures = tracker.failures.filter((at) => now - at < WINDOW_MS)
  tracker.failures.push(now)
  if (tracker.failures.length >= MAX_FAILURES && tracker.lockedAt === null) {
    tracker.lockedAt = now
  }
}

export function forgetFailures(tracker: Tracker): void {
  Object.assign(tracker, freshTracker())
}

/** The 429 init with Retry-After for a locked tracker. */
export function lockedInit(retryAfter: number) {
  return { headers: { ...PROBLEM.headers, 'Retry-After': String(retryAfter) } }
}

export const tooManyAttempts = problem(429, 'Too Many Requests', 'too_many_attempts')
