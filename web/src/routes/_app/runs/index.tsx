// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Runs } from '../../../pages/Runs'
import { parseRunFilter } from '../../../runFilter'

export const Route = createFileRoute('/_app/runs/')({
  // The filter lives in the address: sourceId and any number of status; what is not valid is dropped.
  validateSearch: (search) => {
    const { statuses, sourceId } = parseRunFilter(search)
    return {
      ...(statuses.length > 0 ? { status: statuses } : {}),
      ...(sourceId === null ? {} : { sourceId }),
    }
  },
  component: Runs,
})
