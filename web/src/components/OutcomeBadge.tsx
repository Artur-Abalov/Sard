// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { useTranslation } from 'react-i18next'
import type { components } from '../api/schema'
import { useFormat } from '../useFormat'
import { StatusBadge } from './StatusBadge'

type Outcome = components['schemas']['RunOutcome']

// What a run came to, in words: the server's outcome, with the deadline it waits for or the timeout it
// ran over when the run has one. Nothing is decided here, the words follow the outcome.
export function OutcomeBadge({
  run,
  timeoutSeconds = null,
}: {
  // The deadline of a wait comes with the run, a list row has it too (RunSummary).
  run: { outcome: Outcome; agentWaitUntil?: string | null }
  timeoutSeconds?: number | null
}) {
  const { t } = useTranslation()
  const format = useFormat()
  const plain = t(`enum.RunOutcome.${run.outcome}`)
  let text = plain
  if (run.outcome === 'waiting_for_agent' && run.agentWaitUntil) {
    text = t('runs.waitingUntil', { time: format.time(run.agentWaitUntil) })
  }
  if (run.outcome === 'timed_out' && timeoutSeconds !== null) {
    text = `${plain} (${format.timeout(timeoutSeconds)})`
  }
  return <StatusBadge group="RunOutcome" value={run.outcome} text={text} />
}
