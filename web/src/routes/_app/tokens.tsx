// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Tokens } from '../../pages/Tokens'

export const Route = createFileRoute('/_app/tokens')({
  // create: open the new token window; hint=enroll: explain the enroll command.
  validateSearch: (search): { create?: true; hint?: 'enroll' } => ({
    ...(search.create === true || search.create === 'true' ? { create: true as const } : {}),
    ...(search.hint === 'enroll' ? { hint: 'enroll' as const } : {}),
  }),
  component: Tokens,
})
