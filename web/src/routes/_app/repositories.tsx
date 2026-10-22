// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Repositories } from '../../pages/Repositories'

export const Route = createFileRoute('/_app/repositories')({
  component: Repositories,
})
