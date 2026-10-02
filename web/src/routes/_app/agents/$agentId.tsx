// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Agent } from '../../../pages/Agent'

export const Route = createFileRoute('/_app/agents/$agentId')({
  component: Agent,
})
