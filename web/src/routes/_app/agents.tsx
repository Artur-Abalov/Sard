// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Agents } from '../../pages/Agents'

export const Route = createFileRoute('/_app/agents')({
  component: Agents,
})
