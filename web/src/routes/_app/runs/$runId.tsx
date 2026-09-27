// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Run } from '../../../pages/Run'

export const Route = createFileRoute('/_app/runs/$runId')({
  component: Run,
})
