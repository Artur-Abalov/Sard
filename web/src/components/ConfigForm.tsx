// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Code, Select, Stack, Switch, Text, Textarea, TextInput } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { ErrorText } from '../fieldErrors'
import {
  fieldText,
  type FieldModel,
  type FieldValue,
  type FormModel,
  type FormValues,
} from '../schemaForm'
import { StringListEditor } from './StringListEditor'
import { useErrorText } from './useErrorText'

type Errors = Record<string, ErrorText[]>

function elementErrors(name: string, errors: Errors, text: ReturnType<typeof useErrorText>) {
  const prefix = `config:${name}/`
  return Object.fromEntries(
    Object.entries(errors)
      .filter(([key]) => key.startsWith(prefix))
      .map(([key, texts]) => [Number(key.slice(prefix.length)), text(texts) ?? '']),
  )
}

interface FieldProps {
  field: FieldModel
  value: FieldValue
  onChange: (value: FieldValue) => void
  secretNames: string[]
  errors: Errors
}

function useFieldCommon(field: FieldModel, errors: Errors) {
  const { i18n } = useTranslation()
  const text = useErrorText()
  const { title, description, example } = fieldText(field.name, field.property, i18n.language)
  const error = text(errors[`config:${field.name}`])
  return { title, description, example, error, text }
}

function chosen(value: FieldValue): string | null {
  return typeof value === 'string' && value !== '' ? value : null
}

function SwitchField({ field, value, onChange, errors }: FieldProps) {
  const { title, description, error } = useFieldCommon(field, errors)
  return (
    <Switch
      label={title}
      description={description ?? undefined}
      error={error}
      checked={value === true}
      onChange={(event) => onChange(event.currentTarget.checked)}
    />
  )
}

function ListField({ field, value, onChange, errors }: FieldProps) {
  const { title, description, example, error, text } = useFieldCommon(field, errors)
  return (
    <StringListEditor
      label={title}
      description={description}
      placeholder={example}
      required={field.required}
      values={Array.isArray(value) ? value : []}
      onChange={onChange}
      error={error}
      elementErrors={elementErrors(field.name, errors, text)}
    />
  )
}

// A choice among names: the host's secrets for a secret field (never a value), the enum otherwise.
function ChoiceField({ field, value, onChange, secretNames, errors }: FieldProps) {
  const { t } = useTranslation()
  const { title, description, error } = useFieldCommon(field, errors)
  return (
    <Select
      label={title}
      description={description ?? undefined}
      error={error}
      withAsterisk={field.required}
      clearable
      placeholder={field.kind === 'secret' ? t('form.chooseSecret') : undefined}
      data={field.kind === 'secret' ? secretNames : field.enumValues}
      value={chosen(value)}
      onChange={(name) => onChange(name ?? '')}
    />
  )
}

function TextField({ field, value, onChange, errors }: FieldProps) {
  const { title, description, example, error } = useFieldCommon(field, errors)
  return (
    <TextInput
      label={title}
      description={description ?? undefined}
      error={error}
      withAsterisk={field.required}
      placeholder={example ?? undefined}
      inputMode={field.kind === 'string' ? undefined : 'decimal'}
      value={typeof value === 'string' ? value : ''}
      onChange={(event) => onChange(event.currentTarget.value)}
    />
  )
}

function FieldInput(props: FieldProps) {
  switch (props.field.kind) {
    case 'boolean':
      return <SwitchField {...props} />
    case 'stringArray':
      return <ListField {...props} />
    case 'secret':
    case 'enum':
      return <ChoiceField {...props} />
    default:
      return <TextField {...props} />
  }
}

// The fields of a plugin's config, laid out from its schema. Nothing is checked here:
// required fields are marked, and the server says what it refuses.
export function ConfigForm({
  model,
  values,
  onChange,
  secretNames,
  errors,
}: {
  model: Extract<FormModel, { kind: 'form' }>
  values: FormValues
  onChange: (values: FormValues) => void
  secretNames: string[]
  errors: Errors
}) {
  const text = useErrorText()
  const whole = text(errors.config)
  return (
    <Stack>
      {whole && (
        <Text c="red" role="alert">
          {whole}
        </Text>
      )}
      {model.fields.map((field) => (
        <FieldInput
          key={field.name}
          field={field}
          value={values[field.name]}
          secretNames={secretNames}
          errors={errors}
          onChange={(value) => onChange({ ...values, [field.name]: value })}
        />
      ))}
    </Stack>
  )
}

// The whole config as JSON, for a schema the form cannot lay out; errors of any config path land here.
export function ConfigJson({
  schema,
  json,
  onChange,
  errors,
}: {
  schema: Record<string, unknown>
  json: string
  onChange: (json: string) => void
  errors: Errors
}) {
  const { t } = useTranslation()
  const text = useErrorText()
  const own = Object.entries(errors).filter(
    ([key]) => key === 'config' || key.startsWith('config:'),
  )
  return (
    <Stack>
      <Text size="sm">{t('form.jsonConfig')}</Text>
      <Textarea
        label={t('form.config')}
        autosize
        minRows={4}
        value={json}
        onChange={(event) => onChange(event.currentTarget.value)}
        error={text(own.flatMap(([, texts]) => texts))}
      />
      <Text size="xs" c="dimmed">
        {t('form.schema')}
      </Text>
      <Code block>{JSON.stringify(schema, null, 2)}</Code>
    </Stack>
  )
}
