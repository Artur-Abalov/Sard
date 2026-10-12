// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Settings } from '../../pages/Settings'

export const Route = createFileRoute('/_app/settings')({
  component: Settings,
})
