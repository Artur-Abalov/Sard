// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { SourceNew } from '../../../pages/SourceForm'

export const Route = createFileRoute('/_app/sources/new')({
  // agentId: the agent the form starts with (the agent card's "Create source").
  validateSearch: (search): { agentId?: string } =>
    typeof search.agentId === 'string' ? { agentId: search.agentId } : {},
  component: SourceNew,
})
