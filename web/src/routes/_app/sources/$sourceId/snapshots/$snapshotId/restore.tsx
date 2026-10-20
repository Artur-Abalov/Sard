// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Restore } from '../../../../../../pages/Restore'
import { parseRestoreSearch } from '../../../../../../restoreView'

export const Route = createFileRoute('/_app/sources/$sourceId/snapshots/$snapshotId/restore')({
  // agentId and config come with the retry of a restore run; what is not valid is dropped.
  validateSearch: (search) => {
    const { agentId, config } = parseRestoreSearch(search)
    return {
      ...(agentId === null ? {} : { agentId }),
      ...(config === null ? {} : { config: JSON.stringify(config) }),
    }
  },
  component: Restore,
})
