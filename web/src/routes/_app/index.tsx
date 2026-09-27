// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Dashboard } from '../../pages/Dashboard'

export const Route = createFileRoute('/_app/')({
  component: Dashboard,
})
