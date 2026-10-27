// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Tokens } from '../../pages/Tokens'

export const Route = createFileRoute('/_app/tokens')({
  // create: open the new token window; hint=enroll: explain the enroll command.
  // role=verifier: the window opens with the role of a verifier chosen (the instruction for the first verifier).
  validateSearch: (search): { create?: true; hint?: 'enroll'; role?: 'verifier' } => ({
    ...(search.create === true || search.create === 'true' ? { create: true as const } : {}),
    ...(search.hint === 'enroll' ? { hint: 'enroll' as const } : {}),
    ...(search.role === 'verifier' ? { role: 'verifier' as const } : {}),
  }),
  component: Tokens,
})
