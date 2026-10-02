// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import { buildChecklist } from './checklist'

const none = {
  tokenIssued: false,
  agentConnected: false,
  repositoryInitialized: false,
  sourceCreated: false,
  backupSucceeded: false,
  complete: false,
}

describe('the first steps checklist', () => {
  test('has five items in the order of the first backup', () => {
    expect(buildChecklist(none)?.map((item) => item.step)).toEqual([
      'tokenIssued',
      'agentConnected',
      'repositoryInitialized',
      'sourceCreated',
      'backupSucceeded',
    ])
  })

  test('takes its marks from the server only', () => {
    // Agents, sources and runs may all exist: the checklist knows nothing of them.
    expect(buildChecklist(none)?.every((item) => !item.done)).toBe(true)
    expect(
      buildChecklist({ ...none, tokenIssued: true, agentConnected: true })?.map((i) => i.done),
    ).toEqual([true, true, false, false, false])
  })

  test('is hidden once the server says it is complete', () => {
    const all = { ...none, tokenIssued: true, complete: true }

    expect(buildChecklist(all)).toBeNull()
  })

  test('an unfinished step leads to its own action', () => {
    expect(buildChecklist(none)?.map((item) => item.target)).toEqual([
      { to: '/tokens', search: { create: true } },
      { to: '/tokens', search: { hint: 'enroll' } },
      { to: '/agents', search: { hint: 'repo-init' } },
      { to: '/sources/new' },
      { to: '/sources', search: { hint: 'run-backup' } },
    ])
  })
})
