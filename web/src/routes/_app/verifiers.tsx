// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Verifiers } from '../../pages/Verifiers'

export const Route = createFileRoute('/_app/verifiers')({
  component: Verifiers,
})
