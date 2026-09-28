// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import createClient from 'openapi-fetch'
import type { components, paths } from './schema'

export type Status = components['schemas']['StatusResponse']

// Same-origin requests; in development Vite proxies /api to the server. The
// origin is spelled out because fetch outside a page (Vitest) has no base URL.
export const client = createClient<paths>({ baseUrl: location.origin })

export async function fetchStatus(): Promise<Status> {
  const { data, error } = await client.GET('/api/v1/status')
  if (error !== undefined || data === undefined) {
    throw new Error('GET /api/v1/status failed')
  }
  return data
}
