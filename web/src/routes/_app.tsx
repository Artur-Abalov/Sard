// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { guard } from '../auth/guard'
import { Layout } from '../Layout'

// Pathless layout of every page that needs a signed-in user. Its beforeLoad
// runs before any child loader, so a refused visitor starts no data requests.
export const Route = createFileRoute('/_app')({
  beforeLoad: ({ location }) => guard(location),
  component: Layout,
})
