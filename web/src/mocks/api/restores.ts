// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { http } from '../http'
import { noSession, notFound, PROBLEM } from '../problems'
import {
  isChoice,
  previewOf,
  queuedRestore,
  resolveRestore,
  restoreOptionsOf,
  snapshotOf,
} from '../restores'
import { onboardingIncomplete, refusedUntilComplete } from '../restriction'
import { state } from '../state'

export const restoreHandlers = [
  http.get(
    '/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restore-options',
    ({ params, response }) => {
      if (!state.signedIn) return response(401).json(noSession, PROBLEM)
      const found = snapshotOf(params.sourceId, params.snapshotId)
      if (found === undefined) return response(404).json(notFound, PROBLEM)
      return response(200).json(restoreOptionsOf(found.source, found.snapshot))
    },
  ),

  http.post(
    '/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restores/preview',
    async ({ params, request, response }) => {
      if (!state.signedIn) return response(401).json(noSession, PROBLEM)
      if (refusedUntilComplete()) return response(409).json(onboardingIncomplete, PROBLEM)
      const found = snapshotOf(params.sourceId, params.snapshotId)
      if (found === undefined) return response(404).json(notFound, PROBLEM)
      const choice = resolveRestore(found, await request.json())
      if (isChoice(choice)) return response(200).json(previewOf(choice))
      return choice.status === 409
        ? response(409).json(choice.body, PROBLEM)
        : response(422).json(choice.body, PROBLEM)
    },
  ),

  http.post(
    '/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restores',
    async ({ params, request, response }) => {
      if (!state.signedIn) return response(401).json(noSession, PROBLEM)
      if (refusedUntilComplete()) return response(409).json(onboardingIncomplete, PROBLEM)
      const found = snapshotOf(params.sourceId, params.snapshotId)
      if (found === undefined) return response(404).json(notFound, PROBLEM)
      const choice = resolveRestore(found, await request.json())
      if (!isChoice(choice)) {
        return choice.status === 409
          ? response(409).json(choice.body, PROBLEM)
          : response(422).json(choice.body, PROBLEM)
      }
      const run = queuedRestore(choice)
      state.runs.unshift(run)
      return response(201).json(run)
    },
  ),
]
