// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute, redirect } from '@tanstack/react-router'
import { setupRedirect } from '../auth/loginGuard'
import { Setup } from '../pages/Setup'

// The first-start wizard: outside the guard, like /login. Once the administrator exists a visitor
// without a session goes to sign-in (Рк2), and a signed-in one goes home when the onboarding is complete.
export const Route = createFileRoute('/setup')({
  beforeLoad: async () => {
    const target = await setupRedirect()
    if (target !== null) throw redirect({ to: target })
  },
  component: Setup,
})
