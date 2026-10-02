// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { useInfiniteQuery } from '@tanstack/react-query'

interface Page<T> {
  items: T[]
  nextCursor: string | null
}

export type Paged<T> = ReturnType<typeof usePaged<T>>

// A cursor-paged list: items in the order of the server's answers, next pages by nextCursor.
// [interval] decides from the items shown whether to ask again (false: do not).
export function usePaged<T>(
  key: readonly unknown[],
  fetchPage: (cursor: string | null) => Promise<Page<T>>,
  interval?: (items: T[]) => number | false,
) {
  const query = useInfiniteQuery({
    queryKey: key,
    queryFn: ({ pageParam }) => fetchPage(pageParam),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.nextCursor,
    refetchInterval: (q) => interval?.(q.state.data?.pages.flatMap((p) => p.items) ?? []) ?? false,
  })
  const items = query.data?.pages.flatMap((page) => page.items)
  return { query, items }
}
