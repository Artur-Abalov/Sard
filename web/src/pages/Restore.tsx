// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Button, Card, Code, Group, List, Select, Stack, Text, Title } from '@mantine/core'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { ApiError, call } from '../api/call'
import { client } from '../api/client'
import {
  restoreOptionsQuery,
  type RestoreOptions,
  type RestorePreview,
  type RestoreTarget,
} from '../api/queries'
import { ConfigForm, ConfigJson } from '../components/ConfigForm'
import { CopyBox } from '../components/CopyBox'
import { ErrorBlock } from '../components/ErrorBlock'
import { AppLink } from '../components/links'
import { Loaded } from '../components/Loaded'
import { Mono } from '../components/Mono'
import { Time } from '../components/Time'
import { fieldFailures } from '../errors'
import { groupFieldErrors } from '../fieldErrors'
import { parseRestoreSearch, restorePreviewStale } from '../restoreView'
import {
  buildConfig,
  buildFormModel,
  configToJson,
  parseConfigJson,
  valuesFromConfig,
  type FormValues,
} from '../schemaForm'
import { tones } from '../theme'
import { useFormat } from '../useFormat'

const route = getRouteApi('/_app/sources/$sourceId/snapshots/$snapshotId/restore')

type Config = Record<string, unknown>

// What the check and the start POST.
interface RestoreRequest {
  agentId: string
  config: Config
}

// What the form holds for the chosen agent: its values for a form schema, the JSON text otherwise.
interface Draft {
  agentId: string
  values: FormValues
  json: string
}

function draftFor(target: RestoreTarget, config: Config | null): Draft {
  const model = buildFormModel(target.restoreConfigSchema)
  const start = config ?? target.defaults
  return {
    agentId: target.agentId,
    values: model.kind === 'form' ? valuesFromConfig(model, start) : {},
    json: configToJson(start),
  }
}

// The config the draft stands for; null while the JSON editor holds something that is not an object.
function configOf(target: RestoreTarget, draft: Draft): Config | null {
  const model = buildFormModel(target.restoreConfigSchema)
  if (model.kind === 'form') return buildConfig(model, draft.values)
  const parsed = parseConfigJson(draft.json)
  return parsed.ok ? parsed.config : null
}

function Snapshot({ options }: { options: RestoreOptions }) {
  const format = useFormat()
  const { snapshot } = options
  return (
    <Card withBorder>
      <Stack gap={4}>
        <Title order={4}>{snapshot.sourceName}</Title>
        <Group>
          <Code>{snapshot.snapshotId}</Code>
          <Time value={snapshot.createdAt} />
          <Text size="sm">
            <Trans
              i18nKey="restore.total"
              values={{ size: format.bytes(snapshot.totalBytes) }}
              components={{ m: <Mono /> }}
            />
          </Text>
        </Group>
        <Text size="sm">
          <Trans
            i18nKey="run.repository"
            values={{ name: snapshot.repositoryName }}
            components={{ m: <Mono /> }}
          />
        </Text>
      </Stack>
    </Card>
  )
}

function Ineligible({ options }: { options: RestoreOptions }) {
  const { t } = useTranslation()
  if (options.ineligible.length === 0) return null
  return (
    <Stack gap={4}>
      <Text fw={500}>{t('restore.ineligibleTitle')}</Text>
      <List>
        {options.ineligible.map((agent) => (
          <List.Item key={agent.agentId}>
            <Mono>{agent.hostname}</Mono> — {t(`restore.ineligible.${agent.reason}`)}
          </List.Item>
        ))}
      </List>
    </Stack>
  )
}

// No agent can restore it: say what is missing and give the command the server built, as sent.
function NoTargets({ options }: { options: RestoreOptions }) {
  const { t } = useTranslation()
  return (
    <Stack>
      <Alert color={tones.info}>{t('restore.noTargets')}</Alert>
      <CopyBox value={options.attachHint.command} label={t('restore.attachCommand')} />
      <Ineligible options={options} />
    </Stack>
  )
}

function targetLabel(target: RestoreTarget, t: (key: string) => string): string {
  const marks = [
    target.sourceAgent ? t('restore.sourceAgent') : null,
    target.online ? null : t('restore.offline'),
  ].filter((mark) => mark !== null)
  return marks.length === 0 ? target.hostname : `${target.hostname} (${marks.join(', ')})`
}

function Preview({ preview }: { preview: RestorePreview }) {
  const { t } = useTranslation()
  return (
    <Card withBorder>
      <Stack gap="xs">
        <Title order={5}>{t('restore.previewTitle')}</Title>
        <Text>
          <Trans
            i18nKey="restore.willGoTo"
            values={{ host: preview.hostname, target: preview.target }}
            components={{ m: <Mono /> }}
          />
        </Text>
        {preview.paths.length > 0 && (
          <Text size="sm">
            {t('restore.paths')} <Mono>{preview.paths.join(', ')}</Mono>
          </Text>
        )}
        {preview.warnings.map((code) => (
          <Alert key={code} color={tones.warning} role="status">
            {t(`restore.warning.${code}`)}
          </Alert>
        ))}
      </Stack>
    </Card>
  )
}

interface Previewed extends RestoreRequest {
  preview: RestorePreview
}

// The preview and the start POST the same body; a refusal is kept for the form to show.
function useRestoreCalls(sourceId: string, snapshotId: string) {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const path = { params: { path: { sourceId, snapshotId } } }
  const check = useMutation({
    mutationFn: (body: RestoreRequest) =>
      call(
        client.POST('/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restores/preview', {
          ...path,
          body,
        }),
      ).then((preview): Previewed => ({ ...body, preview })),
  })
  const start = useMutation({
    mutationFn: (body: RestoreRequest) =>
      call(
        client.POST('/api/v1/sources/{sourceId}/snapshots/{snapshotId}/restores', {
          ...path,
          body,
        }),
      ),
    onSuccess: async (run) => {
      void queryClient.invalidateQueries({ queryKey: ['runs'] })
      await navigate({ to: '/runs/$runId', params: { runId: run.id } })
    },
  })
  return { check, start }
}

function AgentSelect({
  options,
  agentId,
  onPick,
  invalid,
}: {
  options: RestoreOptions
  agentId: string
  onPick: (target: RestoreTarget) => void
  invalid: boolean
}) {
  const { t } = useTranslation()
  return (
    <Select
      label={t('restore.agent')}
      allowDeselect={false}
      data={options.targets.map((x) => ({ value: x.agentId, label: targetLabel(x, t) }))}
      value={agentId}
      onChange={(id) => {
        const next = options.targets.find((x) => x.agentId === id)
        if (next) onPick(next)
      }}
      error={invalid ? t('errors.validation_failed') : undefined}
    />
  )
}

type Errors = ReturnType<typeof groupFieldErrors>

// The config of the chosen agent: its form, or the JSON of the whole config for a schema the form cannot lay out.
function ConfigFields({
  target,
  draft,
  onChange,
  errors,
}: {
  target: RestoreTarget
  draft: Draft
  onChange: (draft: Draft) => void
  errors: Errors
}) {
  const model = buildFormModel(target.restoreConfigSchema)
  if (model.kind === 'json') {
    return (
      <ConfigJson
        schema={target.restoreConfigSchema}
        json={draft.json}
        onChange={(json) => onChange({ ...draft, json })}
        errors={errors}
      />
    )
  }
  return (
    <ConfigForm
      model={model}
      values={draft.values}
      onChange={(values) => onChange({ ...draft, values })}
      secretNames={[]}
      errors={errors}
    />
  )
}

// "Check" always; "Start" only for a preview of exactly what the form holds now.
function Actions({
  request,
  check,
  start,
  ready,
}: {
  request: RestoreRequest | null
  check: { isPending: boolean; mutate: (body: RestoreRequest) => void }
  start: { isPending: boolean; mutate: (body: RestoreRequest) => void }
  ready: boolean
}) {
  const { t } = useTranslation()
  return (
    <Group>
      <Button
        variant="default"
        loading={check.isPending}
        disabled={request === null}
        onClick={() => request && check.mutate(request)}
      >
        {t('restore.check')}
      </Button>
      {ready && (
        <Button loading={start.isPending} onClick={() => request && start.mutate(request)}>
          {t('restore.start')}
        </Button>
      )}
    </Group>
  )
}

function startingDraft(
  options: RestoreOptions,
  initial: { agentId: string | null; config: Config | null },
): Draft {
  const first = options.targets.find((x) => x.agentId === initial.agentId)
  return draftFor(first ?? options.targets[0], first ? initial.config : null)
}

// What a refused check says, at the fields it names; `count` is 0 when it named none.
function refusal(error: unknown, target: RestoreTarget): { errors: Errors; count: number } {
  const failures = error instanceof ApiError ? fieldFailures(error.body) : []
  const model = buildFormModel(target.restoreConfigSchema)
  const names = model.kind === 'form' ? model.fields.map((f) => f.name) : []
  return { errors: groupFieldErrors(failures, names), count: failures.length }
}

// What the server said to the last check and start: a refusal that names no field, the preview.
function Answers({
  checkError,
  preview,
  startError,
}: {
  checkError: unknown
  preview: Previewed | null
  startError: unknown
}) {
  return (
    <>
      {checkError !== null && <ErrorBlock error={checkError} />}
      {preview !== null && <Preview preview={preview.preview} />}
      {startError !== null && <ErrorBlock error={startError} />}
    </>
  )
}

function RestoreForm({
  options,
  sourceId,
  initial,
}: {
  options: RestoreOptions
  sourceId: string
  initial: { agentId: string | null; config: Config | null }
}) {
  const [draft, setDraft] = useState(() => startingDraft(options, initial))
  const target = options.targets.find((x) => x.agentId === draft.agentId) ?? options.targets[0]
  const { check, start } = useRestoreCalls(sourceId, options.snapshot.snapshotId)
  const config = configOf(target, draft)
  const request = config === null ? null : { agentId: draft.agentId, config }
  const previewed = check.data ?? null
  const ready = request !== null && !restorePreviewStale(previewed, request)
  const { errors, count } = refusal(check.error, target)
  return (
    <Stack>
      <AgentSelect
        options={options}
        agentId={draft.agentId}
        onPick={(next) => setDraft(draftFor(next, null))}
        invalid={errors.agentId !== undefined}
      />
      <Text size="sm">
        <Trans
          i18nKey="restore.dir"
          values={{ dir: target.restoreDir }}
          components={{ m: <Mono /> }}
        />
      </Text>
      <ConfigFields target={target} draft={draft} onChange={setDraft} errors={errors} />
      <Answers
        checkError={count === 0 ? check.error : null}
        preview={ready ? previewed : null}
        startError={start.error}
      />
      <Actions request={request} check={check} start={start} ready={ready} />
      <Ineligible options={options} />
    </Stack>
  )
}

export function Restore() {
  const { t } = useTranslation()
  const { sourceId, snapshotId } = route.useParams()
  const search = parseRestoreSearch(route.useSearch())
  const options = useQuery(restoreOptionsQuery(sourceId, snapshotId))
  return (
    <Loaded
      query={options}
      back={
        <AppLink to="/sources/$sourceId" params={{ sourceId }}>
          {t('restore.backToSource')}
        </AppLink>
      }
    >
      {(data) => (
        <Stack>
          <Title order={2}>{t('restore.title')}</Title>
          <Snapshot options={data} />
          {data.targets.length === 0 ? (
            <NoTargets options={data} />
          ) : (
            <RestoreForm options={data} sourceId={sourceId} initial={search} />
          )}
        </Stack>
      )}
    </Loaded>
  )
}
