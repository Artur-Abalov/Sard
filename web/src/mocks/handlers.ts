// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { agentHandlers } from './api/agents'
import { runHandlers } from './api/runs'
import { sessionHandlers } from './api/session'
import { sourceHandlers } from './api/sources'
import { tokenHandlers } from './api/tokens'
import { status } from './fixtures'
import { http } from './http'
import { originGuardHandlers } from './origin'

// Default responses shared by the dev service worker and the Vitest server.
// Every endpoint but status needs the mock session (state.ts). The Origin guard
// runs first so a foreign Origin never reaches a resource handler (К1, К4).
export const handlers = [
  ...originGuardHandlers,
  http.get('/api/v1/status', ({ response }) => response(200).json(status)),
  ...sessionHandlers,
  ...agentHandlers,
  ...tokenHandlers,
  ...sourceHandlers,
  ...runHandlers,
]
