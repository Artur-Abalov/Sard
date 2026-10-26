// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Source } from '../../../../pages/Source'

export const Route = createFileRoute('/_app/sources/$sourceId/')({
  // tab=verifications: the tab of the source's restore verifications.
  validateSearch: (search): { tab?: 'verifications' } =>
    search.tab === 'verifications' ? { tab: 'verifications' } : {},
  component: Source,
})
