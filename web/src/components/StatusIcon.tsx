// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { ReactNode } from 'react'
import type { IconName } from '../theme'

const SIZE = 14

// The drawings behind the status icons, on a 16x16 grid; they take the text color.
const drawings: Record<IconName, ReactNode> = {
  check: <path d="M3 8.5 6.5 12 13 4.5" />,
  'x-circle': (
    <>
      <circle cx="8" cy="8" r="6.25" />
      <path d="m5.5 5.5 5 5m0-5-5 5" />
    </>
  ),
  'x-square': (
    <>
      <rect x="2.75" y="2.75" width="10.5" height="10.5" rx="2" />
      <path d="m5.75 5.75 4.5 4.5m0-4.5-4.5 4.5" />
    </>
  ),
  clock: (
    <>
      <circle cx="8" cy="8" r="6.25" />
      <path d="M8 4.5V8l2.5 1.5" />
    </>
  ),
  'clock-dashed': (
    <>
      <circle cx="8" cy="8" r="6.25" strokeDasharray="2 2" />
      <path d="M8 4.5V8l2.5 1.5" />
    </>
  ),
  hourglass: (
    <path d="M4.5 2.5h7m-7 11h7M5 2.5c0 3 3 4 3 5.5s-3 2.5-3 5.5m6-11c0 3-3 4-3 5.5s3 2.5 3 5.5" />
  ),
  arrow: <path d="M3 8h9.5M8.5 4l4 4-4 4" />,
  play: <path d="M5 3.5 12.5 8 5 12.5z" />,
  stop: <rect x="4" y="4" width="8" height="8" rx="1" />,
  dot: <circle cx="8" cy="8" r="3.5" fill="currentColor" />,
  circle: <circle cx="8" cy="8" r="4.5" />,
  minus: <path d="M4.5 8h7" />,
}

// A status icon, 14px, drawn in the color of the text around it.
export function StatusIcon({ name }: { name: IconName }) {
  return (
    <svg
      aria-hidden="true"
      width={SIZE}
      height={SIZE}
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {drawings[name]}
    </svg>
  )
}
