// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Sources } from '../../../pages/Sources'

export const Route = createFileRoute('/_app/sources/')({
  // hint=run-backup: the page explains the "Run backup" button (first steps checklist).
  validateSearch: (search): { hint?: 'run-backup' } =>
    search.hint === 'run-backup' ? { hint: 'run-backup' } : {},
  component: Sources,
})
