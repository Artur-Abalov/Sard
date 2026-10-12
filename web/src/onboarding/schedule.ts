// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// The nightly schedule of the self-backup as the wizard edits it: a time of day, one cron "M H * * *" (Рк12).
// The console only translates; whether a cron is acceptable is the server's to say.

const TIME = /^(\d{2}):(\d{2})$/
const DAILY = /^(\d{1,2}) (\d{1,2}) \* \* \*$/
const MAX_HOUR = 23
const MAX_MINUTE = 59

const pad = (value: number) => String(value).padStart(2, '0')

/** "HH:MM" as the cron "M H * * *", or null when the text is not a time of day. */
export function timeToCron(time: string): string | null {
  const match = TIME.exec(time)
  if (match === null) return null
  const hour = Number(match[1])
  const minute = Number(match[2])
  return hour > MAX_HOUR || minute > MAX_MINUTE ? null : `${minute} ${hour} * * *`
}

/** The cron "M H * * *" as "HH:MM"; null for any other cron, which is shown as it is without a time field. */
export function cronToTime(cron: string): string | null {
  const match = DAILY.exec(cron)
  if (match === null) return null
  const minute = Number(match[1])
  const hour = Number(match[2])
  return hour > MAX_HOUR || minute > MAX_MINUTE ? null : `${pad(hour)}:${pad(minute)}`
}
