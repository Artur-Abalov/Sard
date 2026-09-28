// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { redirect } from '@tanstack/react-router'
import { checkSession, UnauthenticatedError } from './session'

// The page being opened; a subset of the router's ParsedLocation.
export interface Destination {
  pathname: string
  searchStr: string
}

/**
 * Decides whether a protected page may open. Runs in the layout route's beforeLoad,
 * before any loader, so a refused visitor never triggers data requests. No session
 * (401) redirects to /login with the destination remembered; any other failure
 * (network, 5xx) is rethrown as a plain error for the route's errorComponent —
 * a session that cannot be checked is not the same as no session (Р9в).
 */
export async function guard(destination: Destination): Promise<void> {
  try {
    await checkSession()
  } catch (error) {
    if (error instanceof UnauthenticatedError) {
      throw redirect({
        to: '/login',
        search: { redirect: destination.pathname + destination.searchStr },
      })
    }
    throw error
  }
}
