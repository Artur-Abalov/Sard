// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'
import {
  buildConfig,
  buildFormModel,
  configToJson,
  initialValues,
  parseConfigJson,
  valuesFromConfig,
  type FormValues,
} from './schemaForm'

type Schemas = components['schemas']
type Plugin = Schemas['AgentPlugin']

// What the source form holds while it is filled in; nothing in it is checked here, the server checks.
export interface Draft {
  name: string
  agentId: string
  plugin: string
  repository: string
  // The fields of the config form, when the plugin's schema can be laid out.
  values: FormValues
  // The config as JSON text, when it cannot.
  json: string
}

export function emptyDraft(agentId = ''): Draft {
  return { name: '', agentId, plugin: '', repository: '', values: {}, json: '{}' }
}

// Revoked agents cannot be given a source (P6).
export function offeredAgents<T extends { revokedAt: string | null }>(agents: T[]): T[] {
  return agents.filter((agent) => agent.revokedAt === null)
}

// A source of stage 1 backs up: plugins without the backup action are not offered (P6).
export function offeredPlugins(agent: Pick<Schemas['AgentDetails'], 'plugins'>): Plugin[] {
  return agent.plugins.filter((plugin) => plugin.actions.includes('backup'))
}

// Another agent: its plugins and repositories are not the old one's, so those and the config start over.
export function withAgent(draft: Draft, agentId: string): Draft {
  return { ...emptyDraft(agentId), name: draft.name }
}

function startOf(plugin: Plugin): Pick<Draft, 'values' | 'json'> {
  const model = buildFormModel(plugin.configSchema)
  return model.kind === 'form'
    ? { values: initialValues(model), json: '{}' }
    : { values: {}, json: '{}' }
}

// Another plugin: the config starts over, laid out by its schema.
export function withPlugin(draft: Draft, plugin: Plugin): Draft {
  return { ...draft, plugin: plugin.name, ...startOf(plugin) }
}

// The draft of an existing source; its plugin is `plugin` (undefined when the agent no longer offers it).
export function draftOf(
  source: Pick<Schemas['Source'], 'name' | 'agentId' | 'plugin' | 'repositoryName' | 'config'>,
  plugin: Plugin | undefined,
): Draft {
  const base = {
    name: source.name,
    agentId: source.agentId,
    repository: source.repositoryName,
    json: configToJson(source.config),
  }
  if (plugin === undefined) return { ...base, plugin: '', values: {} }
  const model = buildFormModel(plugin.configSchema)
  return {
    ...base,
    plugin: plugin.name,
    values: model.kind === 'form' ? valuesFromConfig(model, source.config) : {},
  }
}

// The body of POST/PUT /sources; not ok only when the JSON editor holds no JSON object.
export function buildSourceInput(
  draft: Draft,
  plugin: Plugin | undefined,
): { ok: true; input: Schemas['SourceInput'] } | { ok: false } {
  const model =
    plugin === undefined ? { kind: 'json' as const } : buildFormModel(plugin.configSchema)
  let config: Record<string, unknown>
  if (model.kind === 'form') {
    config = buildConfig(model, draft.values)
  } else {
    const parsed = parseConfigJson(draft.json)
    if (!parsed.ok) return { ok: false }
    config = parsed.config
  }
  const { name, agentId, plugin: pluginName, repository } = draft
  return {
    ok: true,
    input: { name, agentId, plugin: pluginName, repositoryName: repository, config },
  }
}
