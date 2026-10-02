// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { useQuery } from '@tanstack/react-query'
import { agentQuery } from '../api/queries'
import { shortId } from '../tokens'
import { AppLink } from './links'
import { Mono } from './Mono'

const NAME_STALE_MS = 60_000

// An agent by its host name, linked to its card. The name comes from GET /agents/{id},
// one request per agent for the whole page (the query cache shares it); until it arrives,
// or when it fails, the short id stands in and the link still works (G4).
export function AgentName({ agentId }: { agentId: string }) {
  const agent = useQuery({ ...agentQuery(agentId), staleTime: NAME_STALE_MS })
  return (
    <AppLink to="/agents/$agentId" params={{ agentId }}>
      <Mono>{agent.data?.hostname ?? shortId(agentId)}</Mono>
    </AppLink>
  )
}
