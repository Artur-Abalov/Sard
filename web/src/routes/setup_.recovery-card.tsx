// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute, redirect } from '@tanstack/react-router'
import { checkSession, UnauthenticatedError } from '../auth/session'
import { RecoveryCardPage } from '../pages/RecoveryCard'

// The recovery card during the wizard (Рк15): outside the guard, which sends a visitor to the wizard while the
// server restricts, but still for the administrator only. Without a session the wizard decides where to go.
export const Route = createFileRoute('/setup_/recovery-card')({
  beforeLoad: async () => {
    try {
      await checkSession()
    } catch (error) {
      if (error instanceof UnauthenticatedError) throw redirect({ to: '/setup' })
      throw error
    }
  },
  component: () => <RecoveryCardPage back="/setup" />,
})
