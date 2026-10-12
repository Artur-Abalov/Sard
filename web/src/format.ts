// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import en from './locales/en.json'
import ru from './locales/ru.json'

// Placeholder shown when there is no value yet.
export const EMPTY = '—'

// Formats an ISO-8601 timestamp from the API for display; null -> EMPTY.
export function formatTimestamp(value: string | null, locale: string): string {
  if (value === null) {
    return EMPTY
  }
  return new Date(value).toLocaleString(locale, {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: 'UTC',
  })
}

const dictionaries: Record<string, typeof en> = { en, ru }
const BYTE_UNITS = ['B', 'KiB', 'MiB', 'GiB', 'TiB'] as const
const BINARY = 1024

function dictionary(language: string) {
  return dictionaries[language] ?? en
}

// "{{name}}" placeholders of the locale files, for the pure functions that cannot use i18next.
function fill(template: string, values: Record<string, string>): string {
  return template.replace(/\{\{(\w+)\}\}/g, (_, name: string) => values[name] ?? '')
}

// A byte count in binary units with at most one decimal; null -> EMPTY.
export function formatBytes(bytes: number | null, language: string): string {
  if (bytes === null) {
    return EMPTY
  }
  let value = bytes
  let unit = 0
  while (value >= BINARY && unit < BYTE_UNITS.length - 1) {
    value /= BINARY
    unit += 1
  }
  const number = new Intl.NumberFormat(language, {
    maximumFractionDigits: 1,
    useGrouping: false,
  }).format(value)
  return `${number} ${dictionary(language).units[BYTE_UNITS[unit]]}`
}

// Whole percent of the total, capped at 100; null while the total is unknown.
export function formatPercent(processed: number | null, total: number | null): number | null {
  if (processed === null || total === null || total <= 0) {
    return null
  }
  return Math.min(100, Math.floor((processed / total) * 100))
}

const SECONDS_PER_MINUTE = 60
const SECONDS_PER_HOUR = 3600

// Time from start to end, or to now while the step runs; no start -> EMPTY.
export function formatDuration(
  start: string | null,
  end: string | null,
  now: number,
  language: string,
): string {
  if (start === null) {
    return EMPTY
  }
  const until = end === null ? now : Date.parse(end)
  const seconds = Math.max(0, Math.floor((until - Date.parse(start)) / 1000))
  const { units } = dictionary(language)
  if (seconds >= SECONDS_PER_HOUR) {
    const minutes = Math.floor((seconds % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE)
    return `${Math.floor(seconds / SECONDS_PER_HOUR)} ${units.h} ${minutes} ${units.min}`
  }
  if (seconds >= SECONDS_PER_MINUTE) {
    const rest = seconds % SECONDS_PER_MINUTE
    return `${Math.floor(seconds / SECONDS_PER_MINUTE)} ${units.min} ${rest} ${units.s}`
  }
  return `${seconds} ${units.s}`
}

// "N of M files"; "N files" while the total is unknown; nothing without N.
export function formatFiles(
  processed: number | null,
  total: number | null,
  language: string,
): string | null {
  if (processed === null) {
    return null
  }
  const number = new Intl.NumberFormat(language)
  const { filesCount } = dictionary(language)
  if (total === null) {
    return fill(filesCount.processed, { processed: number.format(processed) })
  }
  return fill(filesCount.ofTotal, {
    processed: number.format(processed),
    total: number.format(total),
  })
}

const SECONDS_PER_DAY = 86_400
// Closer than this to now, either way, is "just now": clocks of browser and server differ a little.
const NOW_WINDOW_SECONDS = 5

// How far a distance in seconds reaches before the next unit takes over.
const RELATIVE_UNITS = [
  { unit: 'd', seconds: SECONDS_PER_DAY },
  { unit: 'h', seconds: SECONDS_PER_HOUR },
  { unit: 'min', seconds: SECONDS_PER_MINUTE },
  { unit: 's', seconds: 1 },
] as const

// A timestamp as its distance from now: "3 h ago", "in 2 h", "just now"; no time -> EMPTY.
// The exact value belongs in a tooltip (formatTimestamp). The unit is the largest that fits, rounded down.
export function formatRelative(value: string | null, now: number, language: string): string {
  const then = value === null ? Number.NaN : Date.parse(value)
  if (Number.isNaN(then)) {
    return EMPTY
  }
  const { relativeTime, units } = dictionary(language)
  const seconds = Math.floor(Math.abs(now - then) / 1000)
  if (seconds < NOW_WINDOW_SECONDS) {
    return relativeTime.now
  }
  const { unit, seconds: size } = RELATIVE_UNITS.find((u) => seconds >= u.seconds)!
  const distance = `${Math.floor(seconds / size)} ${units[unit]}`
  return fill(now >= then ? relativeTime.ago : relativeTime.in, { value: distance })
}

// The time of a schedule is the zone's own, not the browser's, and names the zone (F3b, ВП3).
const SCHEDULE_TIME: Intl.DateTimeFormatOptions = {
  day: 'numeric',
  month: 'short',
  year: 'numeric',
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
}

// "10 Oct 2026, 02:00 Europe/Berlin"; null -> EMPTY.
export function formatScheduleTime(
  value: string | null,
  timeZone: string,
  language: string,
): string {
  if (value === null) {
    return EMPTY
  }
  const time = new Date(value).toLocaleString(language, { ...SCHEDULE_TIME, timeZone })
  return `${time} ${timeZone}`
}

// A count with the space of its language between thousands, a plain one, so that it can be searched for.
function formatCount(count: number, language: string): string {
  return new Intl.NumberFormat(language).format(count).replace(/[  ]/g, ' ')
}

// "4 fires", "21 срабатывание": the noun agrees with the count by the rules of the language.
export function formatFires(count: number, language: string): string {
  const { fires } = dictionary(language).schedule
  const category = new Intl.PluralRules(language).select(count)
  return fill(fires[category as keyof typeof fires], { count: formatCount(count, language) })
}

// The span of missed fires in the zone of the schedule: a day once when both ends fall on it.
function formatPeriod(from: string, until: string, timeZone: string, language: string): string {
  const { period } = dictionary(language).schedule
  const day = (value: string) =>
    new Date(value).toLocaleDateString(language, { dateStyle: 'medium', timeZone })
  const clock = (value: string) =>
    new Date(value).toLocaleTimeString(language, {
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
      timeZone,
    })
  if (day(from) === day(until)) {
    return fill(period.sameDay, {
      from: clock(from),
      until: clock(until),
      date: day(from),
      zone: timeZone,
    })
  }
  const full = (value: string) =>
    new Date(value).toLocaleString(language, { ...SCHEDULE_TIME, timeZone })
  return fill(period.span, { from: full(from), until: full(until), zone: timeZone })
}

// The fires missed while the server was down; [capped]: the count stopped at its limit.
export interface MissedFires {
  from: string
  until: string
  count: number
  capped: boolean
  timezone: string
}

// "the server was down, 4 fires missed from 15:00 to 18:00 (...)". A capped count says "at least"
// and names no end: the last fire counted is not the last one missed.
export function formatMissed(missed: MissedFires, language: string): string {
  const { schedule } = dictionary(language)
  const count = formatFires(missed.count, language)
  if (missed.capped) {
    const from = formatScheduleTime(missed.from, missed.timezone, language)
    return fill(schedule.downtimeCapped, { count, from })
  }
  const period = formatPeriod(missed.from, missed.until, missed.timezone, language)
  return fill(schedule.downtime, { count, period })
}

// The mark of a catch-up run in a list: "catch-up — the server was down, ...".
export function formatCatchUp(missed: MissedFires, language: string): string {
  return fill(dictionary(language).schedule.catchUpMark, { text: formatMissed(missed, language) })
}
