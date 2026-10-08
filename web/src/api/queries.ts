// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { queryOptions } from '@tanstack/react-query'
import { runPollInterval, tokenPollInterval } from '../polling'
import { call } from './call'
import { client } from './client'
import type { InstallChoice } from '../install'
import type { components } from './schema'

type Schemas = components['schemas']

export const PAGE_SIZE = 50
// The most one page may hold: pickers of agents and sources take one such page.
export const PICKER_SIZE = 200

export const overviewQuery = () =>
  queryOptions({
    queryKey: ['overview'],
    queryFn: () => call(client.GET('/api/v1/overview')),
  })

export const agentQuery = (agentId: string) =>
  queryOptions({
    queryKey: ['agent', agentId],
    queryFn: () => call(client.GET('/api/v1/agents/{agentId}', { params: { path: { agentId } } })),
  })

export const sourceQuery = (sourceId: string) =>
  queryOptions({
    queryKey: ['source', sourceId],
    queryFn: () =>
      call(client.GET('/api/v1/sources/{sourceId}', { params: { path: { sourceId } } })),
  })

export const runQuery = (runId: string) =>
  queryOptions({
    queryKey: ['run', runId],
    queryFn: () => call(client.GET('/api/v1/runs/{runId}', { params: { path: { runId } } })),
    // Polled only while the run is active; a failed refresh keeps the last data.
    refetchInterval: (query) => runPollInterval(query.state.data?.status),
  })

export const tokenQuery = (tokenId: string, enabled: boolean) =>
  queryOptions({
    queryKey: ['token', tokenId],
    queryFn: () =>
      call(client.GET('/api/v1/enrollment-tokens/{tokenId}', { params: { path: { tokenId } } })),
    enabled,
    refetchInterval: (query) => tokenPollInterval(query.state.data?.status),
  })

// The CA of this server: its fingerprint ends every enrollment token (F8).
export const caQuery = () =>
  queryOptions({
    queryKey: ['ca'],
    queryFn: () => call(client.GET('/api/v1/ca')),
    staleTime: 60_000,
  })

// How to install an agent from this server (U1b). Versions rarely change: a minute is fresh enough.
export const installQuery = (choice: InstallChoice) =>
  queryOptions({
    queryKey: ['agent-install', choice.arch, choice.format, choice.fetch],
    queryFn: () => call(client.GET('/api/v1/agent-install', { params: { query: choice } })),
    staleTime: 60_000,
  })

export const upgradeQuery = (
  agentId: string,
  format: InstallChoice['format'],
  fetch: InstallChoice['fetch'],
) =>
  queryOptions({
    queryKey: ['agent-upgrade', agentId, format, fetch],
    queryFn: () =>
      call(
        client.GET('/api/v1/agents/{agentId}/upgrade', {
          params: { path: { agentId }, query: { format, fetch } },
        }),
      ),
  })

export const agentPickerQuery = () =>
  queryOptions({
    queryKey: ['agents', 'picker'],
    queryFn: () =>
      call(client.GET('/api/v1/agents', { params: { query: { limit: PICKER_SIZE } } })),
  })

export const sourcePickerQuery = () =>
  queryOptions({
    queryKey: ['sources', 'picker'],
    queryFn: () =>
      call(client.GET('/api/v1/sources', { params: { query: { limit: PICKER_SIZE } } })),
  })

export const firstAgentQuery = () =>
  queryOptions({
    queryKey: ['agents', 'first'],
    queryFn: () => call(client.GET('/api/v1/agents', { params: { query: { limit: 1 } } })),
  })

export const recentRunsQuery = (limit: number) =>
  queryOptions({
    queryKey: ['runs', 'recent', limit],
    queryFn: () => call(client.GET('/api/v1/runs', { params: { query: { limit } } })),
  })

export type Run = Schemas['Run']
export type RunStep = Schemas['RunStep']
export type Source = Schemas['Source']
export type AgentDetails = Schemas['AgentDetails']
export type EnrollmentToken = Schemas['EnrollmentToken']
