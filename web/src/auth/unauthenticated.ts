// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Pages that are open without a session: a 401 there is theirs to handle (Рк7).
const OPEN_PAGES = ['/login', '/setup']

/** Where the global 401 handler sends the console, or null when the page handles it itself. */
export function unauthenticatedTarget(pathname: string, searchStr: string) {
  if (OPEN_PAGES.includes(pathname)) return null
  return { to: '/login', search: { redirect: pathname + searchStr } } as const
}
