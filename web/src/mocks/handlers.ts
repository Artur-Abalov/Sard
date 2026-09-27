// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { status } from './fixtures'
import { http } from './http'

// Default responses shared by the dev service worker and the Vitest server.
export const handlers = [http.get('/api/v1/status', ({ response }) => response(200).json(status))]
