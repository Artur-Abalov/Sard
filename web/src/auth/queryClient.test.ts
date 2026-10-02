// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test, vi } from 'vitest'
import { createAppQueryClient } from './queryClient'
import { UnauthenticatedError } from './session'

describe('createAppQueryClient', () => {
  test('a 401 from a query clears the cache and calls the handler once', async () => {
    const onUnauthenticated = vi.fn()
    const queryClient = createAppQueryClient(onUnauthenticated)
    await queryClient
      .fetchQuery({
        queryKey: ['agents'],
        queryFn: () => Promise.reject(new UnauthenticatedError()),
        retry: false,
      })
      .catch(() => undefined)
    expect(onUnauthenticated).toHaveBeenCalledTimes(1)
    expect(queryClient.getQueryCache().getAll()).toHaveLength(0)
  })

  test('several concurrent 401s call the handler once', async () => {
    const onUnauthenticated = vi.fn()
    const queryClient = createAppQueryClient(onUnauthenticated)
    const fail = (key: string) =>
      queryClient
        .fetchQuery({
          queryKey: [key],
          queryFn: () => Promise.reject(new UnauthenticatedError()),
          retry: false,
        })
        .catch(() => undefined)
    await Promise.all([fail('a'), fail('b'), fail('c')])
    expect(onUnauthenticated).toHaveBeenCalledTimes(1)
  })

  test('an error that is not UnauthenticatedError does not call the handler', async () => {
    const onUnauthenticated = vi.fn()
    const queryClient = createAppQueryClient(onUnauthenticated)
    await queryClient
      .fetchQuery({
        queryKey: ['agents'],
        queryFn: () => Promise.reject(new Error('boom')),
        retry: false,
      })
      .catch(() => undefined)
    expect(onUnauthenticated).not.toHaveBeenCalled()
  })

  test('a 401 from a mutation calls the handler too', async () => {
    const onUnauthenticated = vi.fn()
    const queryClient = createAppQueryClient(onUnauthenticated)
    await queryClient
      .getMutationCache()
      .build(queryClient, { mutationFn: () => Promise.reject(new UnauthenticatedError()) })
      .execute(undefined)
      .catch(() => undefined)
    expect(onUnauthenticated).toHaveBeenCalledTimes(1)
  })
})
