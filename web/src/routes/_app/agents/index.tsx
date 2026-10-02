// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Agents } from '../../../pages/Agents'

export const Route = createFileRoute('/_app/agents/')({
  // hint=repo-init: the page explains how to initialize a repository (first steps checklist).
  validateSearch: (search): { hint?: 'repo-init' } =>
    search.hint === 'repo-init' ? { hint: 'repo-init' } : {},
  component: Agents,
})
