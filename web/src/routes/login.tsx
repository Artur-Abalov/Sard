// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute, redirect } from '@tanstack/react-router'
import { redirectIfSignedIn } from '../auth/loginGuard'
import { Login } from '../pages/Login'

interface LoginSearch {
  redirect?: string
}

// A signed-in visitor is sent straight to their destination (Р9б); a session check
// failure other than 401 is not sign-in, so the login page opens as usual and the
// form itself reports the server being unavailable when it is submitted (Р9в).
export const Route = createFileRoute('/login')({
  validateSearch: (search: Record<string, unknown>): LoginSearch => ({
    redirect: typeof search.redirect === 'string' ? search.redirect : undefined,
  }),
  beforeLoad: async ({ search }) => {
    const target = await redirectIfSignedIn(search.redirect)
    if (target !== null) throw redirect({ to: target })
  },
  component: Login,
})
