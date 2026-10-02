// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import { call } from '../api/call'
import { client } from '../api/client'
import type { components } from '../api/schema'
import { mergeLogLines } from '../logs'
import { POLL_INTERVAL_MS } from '../polling'

type LogLine = components['schemas']['LogLine']

// The first page and the next ones (P8).
export const LOG_PAGE = 500

export interface StepLog {
  lines: LogLine[]
  nextAfterSeq: number
  hasMore: boolean
  truncated: boolean
  loaded: boolean
  error: unknown
}

const EMPTY_LOG: StepLog = {
  lines: [],
  nextAfterSeq: 0,
  hasMore: false,
  truncated: false,
  loaded: false,
  error: null,
}

// A step's log read page by page: the first page at once, the next ones on request ([more]).
// While the step is active the tail is read every polling interval (a hidden tab waits); when
// the step ends the log is read once more to the end and then left alone.
export function useStepLog(runId: string, stepId: string, active: boolean) {
  const queryClient = useQueryClient()
  const [log, setLog] = useState(EMPTY_LOG)
  const current = useRef(EMPTY_LOG)
  const busy = useRef(false)
  const wasActive = useRef(active)

  const more = useCallback(async (): Promise<void> => {
    if (busy.current) return
    busy.current = true
    const afterSeq = current.current.nextAfterSeq
    try {
      const page = await queryClient.fetchQuery({
        queryKey: ['step-log', stepId, afterSeq],
        staleTime: 0,
        gcTime: 0,
        queryFn: () =>
          call(
            client.GET('/api/v1/runs/{runId}/steps/{stepId}/logs', {
              params: { path: { runId, stepId }, query: { afterSeq, limit: LOG_PAGE } },
            }),
          ),
      })
      current.current = {
        lines: mergeLogLines(current.current.lines, page.items),
        nextAfterSeq: page.nextAfterSeq,
        hasMore: page.hasMore,
        truncated: page.truncated,
        loaded: true,
        error: null,
      }
    } catch (error) {
      current.current = { ...current.current, error }
    } finally {
      busy.current = false
      setLog(current.current)
    }
  }, [queryClient, runId, stepId])

  useEffect(() => {
    void more()
  }, [more])

  useEffect(() => {
    if (!active) return
    const timer = setInterval(() => {
      if (!document.hidden && !current.current.hasMore) void more()
    }, POLL_INTERVAL_MS)
    return () => clearInterval(timer)
  }, [active, more])

  useEffect(() => {
    const ended = wasActive.current && !active
    wasActive.current = active
    if (!ended) return
    void (async () => {
      do {
        await more()
      } while (current.current.hasMore && current.current.error === null)
    })()
  }, [active, more])

  return { log, more }
}
