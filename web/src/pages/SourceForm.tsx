// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Group, Select, Stack, TextInput, Title } from '@mantine/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { ApiError, call } from '../api/call'
import { client } from '../api/client'
import {
  agentPickerQuery,
  agentQuery,
  sourceQuery,
  type AgentDetails,
  type Source,
} from '../api/queries'
import type { components } from '../api/schema'
import { ConfigForm, ConfigJson } from '../components/ConfigForm'
import { useErrorText } from '../components/useErrorText'
import { ErrorBlock } from '../components/ErrorBlock'
import { AppLink } from '../components/links'
import { Loaded } from '../components/Loaded'
import { RepoInitHint } from '../components/RepoInitHint'
import { fieldFailures } from '../errors'
import { groupFieldErrors } from '../fieldErrors'
import { buildFormModel } from '../schemaForm'
import {
  buildSourceInput,
  draftOf,
  emptyDraft,
  offeredAgents,
  offeredPlugins,
  withAgent,
  withPlugin,
  type Draft,
} from '../sourceDraft'

const newRoute = getRouteApi('/_app/sources/new')
const editRoute = getRouteApi('/_app/sources/$sourceId/edit')

type Errors = ReturnType<typeof groupFieldErrors>
type SourceInput = components['schemas']['SourceInput']

interface Props {
  draft: Draft
  onChange: (draft: Draft) => void
  agent: AgentDetails | undefined
  errors: Errors
}

function AgentSelect({ draft, onChange, errors }: Omit<Props, 'agent'>) {
  const { t } = useTranslation()
  const text = useErrorText()
  const agents = useQuery(agentPickerQuery())
  const offered = offeredAgents(agents.data?.items ?? [])
  return (
    <Select
      label={t('sources.agent')}
      withAsterisk
      data={offered.map((agent) => ({ value: agent.id, label: agent.hostname }))}
      value={draft.agentId === '' ? null : draft.agentId}
      onChange={(id) => onChange(withAgent(draft, id ?? ''))}
      error={text(errors.agent)}
    />
  )
}

function PluginSelect({ draft, onChange, agent, errors }: Props) {
  const { t } = useTranslation()
  const text = useErrorText()
  const plugins = agent === undefined ? [] : offeredPlugins(agent)
  return (
    <Select
      label={t('sources.plugin')}
      withAsterisk
      disabled={agent === undefined}
      data={plugins.map((plugin) => plugin.name)}
      value={draft.plugin === '' ? null : draft.plugin}
      onChange={(name) => {
        const plugin = plugins.find((p) => p.name === name)
        if (plugin) onChange(withPlugin(draft, plugin))
      }}
      error={text(errors.plugin)}
    />
  )
}

function RepositorySelect({ draft, onChange, agent, errors }: Props) {
  const { t } = useTranslation()
  const text = useErrorText()
  const repositories = agent?.repositories ?? []
  const chosen = repositories.find((r) => r.name === draft.repository)
  return (
    <Stack gap="xs">
      <Select
        label={t('sources.repository')}
        withAsterisk
        disabled={agent === undefined}
        data={repositories.map((repository) => repository.name)}
        value={draft.repository === '' ? null : draft.repository}
        onChange={(name) => onChange({ ...draft, repository: name ?? '' })}
        error={text(errors.repository)}
      />
      {agent !== undefined && repositories.length === 0 && (
        <Alert color="blue">{t('form.noRepositories')}</Alert>
      )}
      {chosen?.repositoryId === null && (
        <Alert color="yellow" title={t('form.notInitializedTitle')}>
          <RepoInitHint repository={chosen.name} />
        </Alert>
      )}
    </Stack>
  )
}

// The config of the chosen plugin: its form, or the JSON of the whole config when the schema is not a form.
function ConfigSection({ draft, onChange, agent, errors }: Props) {
  const { t } = useTranslation()
  const plugin = agent?.plugins.find((p) => p.name === draft.plugin)
  if (agent === undefined || plugin === undefined) return null
  const model = buildFormModel(plugin.configSchema)
  if (model.kind === 'json') {
    return (
      <ConfigJson
        schema={plugin.configSchema}
        json={draft.json}
        onChange={(json) => onChange({ ...draft, json })}
        errors={errors}
      />
    )
  }
  return (
    <Stack>
      <Title order={4}>{t('form.config')}</Title>
      <ConfigForm
        model={model}
        values={draft.values}
        onChange={(values) => onChange({ ...draft, values })}
        secretNames={agent.secretNames}
        errors={errors}
      />
    </Stack>
  )
}

function Notes({ agent }: { agent: AgentDetails | undefined }) {
  const { t } = useTranslation()
  if (agent === undefined) return null
  return offeredPlugins(agent).length === 0 ? (
    <Alert color="blue">{t('form.noPlugins')}</Alert>
  ) : null
}

// Saves the draft: POST for a new source, PUT for an existing one. The values stay in the form
// whatever the answer, so a retry sends the same ones.
function useSave(sourceId: string | null) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: SourceInput) =>
      sourceId === null
        ? call(client.POST('/api/v1/sources', { body: input }))
        : call(
            client.PUT('/api/v1/sources/{sourceId}', {
              params: { path: { sourceId } },
              body: input,
            }),
          ),
    onSuccess: async (saved) => {
      queryClient.setQueryData(sourceQuery(saved.id).queryKey, saved)
      void queryClient.invalidateQueries({ queryKey: ['sources'] })
      await navigate({ to: '/sources/$sourceId', params: { sourceId: saved.id } })
    },
  })
}

function SaveErrors({ error, failures }: { error: unknown; failures: number }) {
  const { t } = useTranslation()
  if (error === null) return null
  const gone =
    error instanceof ApiError && error.failure.kind === 'problem' && error.failure.status === 404
  return (
    <>
      {failures === 0 && <ErrorBlock error={error} />}
      {gone && <AppLink to="/sources">{t('sources.title')}</AppLink>}
    </>
  )
}

// The state of the source form: the draft, the agent it names, the save and its errors.
function useSourceForm(initial: Draft, sourceId: string | null) {
  const [draft, setDraft] = useState(initial)
  const [jsonRefused, setJsonRefused] = useState(false)
  const agent = useQuery({ ...agentQuery(draft.agentId), enabled: draft.agentId !== '' })
  const save = useSave(sourceId)
  const plugin = agent.data?.plugins.find((p) => p.name === draft.plugin)
  const model = plugin === undefined ? null : buildFormModel(plugin.configSchema)
  const failures = save.error instanceof ApiError ? fieldFailures(save.error.body) : []
  const configFields = model?.kind === 'form' ? model.fields.map((f) => f.name) : []
  const errors = groupFieldErrors(failures, configFields)

  function submit() {
    const built = buildSourceInput(draft, plugin)
    setJsonRefused(!built.ok)
    if (built.ok) save.mutate(built.input)
  }

  return {
    draft,
    setDraft,
    agent: agent.data,
    save,
    errors,
    failures: failures.length,
    jsonRefused,
    submit,
  }
}

// A source cannot be saved for an agent that has no plugin to back up with.
function canSave(agent: AgentDetails | undefined): boolean {
  return agent === undefined || offeredPlugins(agent).length > 0
}

function PluginGone({ draft, missing }: { draft: Draft; missing: string | null }) {
  const { t } = useTranslation()
  if (missing === null || draft.plugin !== '') return null
  return <Alert color="yellow">{t('form.pluginGone', { plugin: missing })}</Alert>
}

// The source form in both its uses: the draft starts from [initial]; [sourceId] is null for a new source.
function SourceEditor({
  initial,
  sourceId,
  missingPlugin,
}: {
  initial: Draft
  sourceId: string | null
  missingPlugin: string | null
}) {
  const { t } = useTranslation()
  const text = useErrorText()
  const form = useSourceForm(initial, sourceId)
  const { draft, errors, agent } = form
  const props = { draft, onChange: form.setDraft, agent, errors }
  return (
    <Stack maw={720}>
      <PluginGone draft={draft} missing={missingPlugin} />
      <TextInput
        label={t('sources.name')}
        withAsterisk
        value={draft.name}
        onChange={(event) => form.setDraft({ ...draft, name: event.currentTarget.value })}
        error={text(errors.name)}
      />
      <AgentSelect draft={draft} onChange={form.setDraft} errors={errors} />
      <Notes agent={agent} />
      <PluginSelect {...props} />
      <RepositorySelect {...props} />
      {errors.form && <Alert color="red">{text(errors.form)}</Alert>}
      <ConfigSection {...props} />
      {form.jsonRefused && <Alert color="red">{t('form.jsonInvalid')}</Alert>}
      <SaveErrors error={form.save.error} failures={form.failures} />
      <Group>
        <Button loading={form.save.isPending} disabled={!canSave(agent)} onClick={form.submit}>
          {t('form.save')}
        </Button>
      </Group>
    </Stack>
  )
}

export function SourceNew() {
  const { t } = useTranslation()
  const { agentId } = newRoute.useSearch()
  return (
    <Stack>
      <Title order={2}>{t('sources.create')}</Title>
      <SourceEditor initial={emptyDraft(agentId ?? '')} sourceId={null} missingPlugin={null} />
    </Stack>
  )
}

// The current values of the source are the draft; the agent's last Register decides whether its plugin is still there.
function EditLoaded({ source, agent }: { source: Source; agent: AgentDetails }) {
  const plugin = offeredPlugins(agent).find((p) => p.name === source.plugin)
  return (
    <SourceEditor
      initial={draftOf(source, plugin)}
      sourceId={source.id}
      missingPlugin={plugin === undefined ? source.plugin : null}
    />
  )
}

function EditAgent({ source }: { source: Source }) {
  const agent = useQuery(agentQuery(source.agentId))
  return <Loaded query={agent}>{(data) => <EditLoaded source={source} agent={data} />}</Loaded>
}

export function SourceEdit() {
  const { t } = useTranslation()
  const { sourceId } = editRoute.useParams()
  const source = useQuery(sourceQuery(sourceId))
  return (
    <Stack>
      <Title order={2}>{t('source.edit')}</Title>
      <Loaded query={source} back={<AppLink to="/sources">{t('sources.title')}</AppLink>}>
        {(data) => <EditAgent source={data} />}
      </Loaded>
    </Stack>
  )
}
