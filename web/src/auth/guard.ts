// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { redirect } from '@tanstack/react-router'
import { fetchOnboarding } from '../onboarding/api'
import { adminPending, type Onboarding } from '../onboarding/state'
import { checkSession, UnauthenticatedError } from './session'

// The page being opened; a subset of the router's ParsedLocation.
export interface Destination {
  pathname: string
  searchStr: string
}

/** Where a visitor without a session goes: the wizard before the admin step, sign-in after it. */
export function unauthenticatedRedirect(onboarding: Onboarding, destination: Destination) {
  return adminPending(onboarding)
    ? ({ to: '/setup' } as const)
    : ({
        to: '/login',
        search: { redirect: destination.pathname + destination.searchStr },
      } as const)
}

/**
 * Decides whether a protected page may open. Runs in the layout route's beforeLoad,
 * before any loader, so a refused visitor never triggers data requests. No session
 * (401) asks the server how far the first start has come: before the admin step it
 * redirects to /setup, after it to /login with the destination remembered. Any other failure
 * (network, 5xx) is rethrown as a plain error for the route's errorComponent —
 * a session that cannot be checked is not the same as no session (Р9в).
 */
export async function guard(destination: Destination): Promise<void> {
  try {
    await checkSession()
  } catch (error) {
    if (error instanceof UnauthenticatedError) {
      throw redirect(unauthenticatedRedirect(await fetchOnboarding(), destination))
    }
    throw error
  }
}
