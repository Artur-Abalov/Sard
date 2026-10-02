// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query'
import { UnauthenticatedError } from './session'

/**
 * The app's QueryClient: a 401 from any query (rule "Ответ 401 во время работы ведёт
 * на вход") clears the cache (Р9д) and calls [onUnauthenticated] once even when
 * several queries fail at the same moment — a microtask-scoped flag coalesces them,
 * since they all reject within the same tick.
 */
export function createAppQueryClient(onUnauthenticated: () => void): QueryClient {
  let handling = false
  const onError = (error: Error) => {
    if (!(error instanceof UnauthenticatedError)) return
    queryClient.clear()
    if (handling) return
    handling = true
    queueMicrotask(() => {
      handling = false
    })
    onUnauthenticated()
  }
  const queryClient: QueryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
    queryCache: new QueryCache({ onError }),
    // A write that meets an ended session (a click on "Run backup") goes to sign-in too.
    mutationCache: new MutationCache({ onError }),
  })
  return queryClient
}
