// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Compile-time checks only, never rendered: `npm run typecheck` fails if a link
// to an existing route stops compiling or a link to a missing one starts to.
import { Link } from '@tanstack/react-router'
import { NavLink } from './NavLink'

export function LinkTypeChecks() {
  return (
    <>
      <Link to="/runs/$runId" params={{ runId: '42' }} />
      {/* @ts-expect-error: there is no /no-such-page route */}
      <Link to="/no-such-page" />
      {/* @ts-expect-error: a run card needs its runId */}
      <Link to="/runs/$runId" />
      <NavLink to="/agents" label="agents" />
      {/* @ts-expect-error: there is no /no-such-page route */}
      <NavLink to="/no-such-page" label="nowhere" />
    </>
  )
}
