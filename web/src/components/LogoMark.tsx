// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

const SIZE = 24

// The logo mark of docs/design/logo.svg: a hexagonal web whose lines follow the text color
// and whose amber node is the accent of the scheme (amber-500 on dark, amber-700 on light),
// so it is drawn inline rather than as an image. The minimum size of the mark is 20px.
export function LogoMark() {
  return (
    <svg aria-hidden="true" width={SIZE} height={SIZE} viewBox="0 0 200 200">
      <g fill="none" stroke="currentColor" strokeWidth="6" strokeLinejoin="round">
        <polygon points="180,100 140,30.7 60,30.7 20,100 60,169.3 140,169.3" />
        <polygon points="130,100 115,74 85,74 70,100 85,126 115,126" />
        <line x1="100" y1="100" x2="192" y2="100" />
        <line x1="100" y1="100" x2="146" y2="20.3" />
        <line x1="100" y1="100" x2="54" y2="20.3" />
        <line x1="100" y1="100" x2="8" y2="100" />
        <line x1="100" y1="100" x2="54" y2="179.7" />
        <line x1="100" y1="100" x2="146" y2="179.7" />
      </g>
      <circle cx="140" cy="30.7" r="14" fill="var(--sard-accent)" />
    </svg>
  )
}
