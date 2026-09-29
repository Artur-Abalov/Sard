// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// A path starting with a single "/" (not "//" or "/\", which browsers treat as
// protocol-relative or backslash-tricked hosts).
const SAFE_INTERNAL_PATH = /^\/(?!\/|\\)\S*$/

/** True when every character is printable: no whitespace and no control character. */
function hasNoControlCharacters(value: string): boolean {
  for (let i = 0; i < value.length; i++) {
    if (value.charCodeAt(i) < 0x20) return false
  }
  return true
}

/**
 * The console's post-sign-in destination from the `redirect` search parameter
 * (Р9а/б): only a path inside the console is honored, so signing in never sends the
 * administrator to an external site. Falls back to "/" for anything else, including
 * a redirect that itself points back to /login (Р9е avoids a redirect loop).
 */
export function resolveRedirectTarget(value: string | undefined): string {
  if (value === undefined || !SAFE_INTERNAL_PATH.test(value) || !hasNoControlCharacters(value)) {
    return '/'
  }
  if (value === '/login' || value.startsWith('/login?') || value.startsWith('/login#')) {
    return '/'
  }
  return value
}
