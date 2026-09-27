// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createFileRoute } from '@tanstack/react-router'
import { Runs } from '../../../pages/Runs'

export const Route = createFileRoute('/_app/runs/')({
  component: Runs,
})
