// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute, redirect } from '@tanstack/react-router'
import { setupFinished } from '../auth/loginGuard'
import { Setup } from '../pages/Setup'

// The first-start wizard: outside the guard, like /login. Once the administrator exists
// there is nothing to set up and the visitor goes to sign-in (Рк2).
export const Route = createFileRoute('/setup')({
  beforeLoad: async () => {
    if (await setupFinished()) throw redirect({ to: '/login' })
  },
  component: Setup,
})
