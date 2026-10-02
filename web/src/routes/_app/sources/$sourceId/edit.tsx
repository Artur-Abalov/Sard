// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { SourceEdit } from '../../../../pages/SourceForm'

export const Route = createFileRoute('/_app/sources/$sourceId/edit')({
  component: SourceEdit,
})
