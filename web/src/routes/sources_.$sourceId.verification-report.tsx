// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { guard, type Destination } from '../auth/guard'
import { RouteError } from '../pages/RouteError'
import { VerificationReport } from '../pages/VerificationReport'

// The report to print: signed-in users only, but outside the shell of the console - no header, no menu.
export const Route = createFileRoute('/sources_/$sourceId/verification-report')({
  // The location is typed by what guard needs: the route's own search type would make the inference circular.
  beforeLoad: ({ location }: { location: Destination }) => guard(location),
  // from, to: dates of the period; without them the server takes the last 30 days.
  validateSearch: (search): { from?: string; to?: string } => ({
    ...(typeof search.from === 'string' ? { from: search.from } : {}),
    ...(typeof search.to === 'string' ? { to: search.to } : {}),
  }),
  component: VerificationReport,
  errorComponent: RouteError,
})
