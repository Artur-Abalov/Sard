// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { QueryClient } from '@tanstack/react-query'
import { createRouter, type RouterHistory } from '@tanstack/react-router'
import { routeTree } from './routeTree.gen'
import { parseSearch, stringifySearch } from './searchParams'

export interface RouterContext {
  queryClient: QueryClient
}

interface Options extends RouterContext {
  // Browser history when omitted; tests pass a memory history.
  history?: RouterHistory
}

export function createAppRouter({ queryClient, history }: Options) {
  return createRouter({
    routeTree,
    history,
    context: { queryClient },
    parseSearch,
    stringifySearch,
    defaultPreload: 'intent',
    // TanStack Query owns freshness, so the router never serves a stale preload.
    defaultPreloadStaleTime: 0,
  })
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>
  }
}
