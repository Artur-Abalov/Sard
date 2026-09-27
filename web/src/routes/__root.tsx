// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { createRootRouteWithContext } from '@tanstack/react-router'
import { NotFound } from '../pages/NotFound'
import { Root } from '../Root'
import type { RouterContext } from '../router'

export const Route = createRootRouteWithContext<RouterContext>()({
  component: Root,
  notFoundComponent: NotFound,
})
