// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// The config form of a source, built from the JSON Schema of its plugin (W2, П3, П4).
// The console only lays the fields out: it checks no value, no bound and no
// requirement; the server answers 422 with the path of each field it refuses.

type Json = Record<string, unknown>

export type FieldKind =
  | 'string'
  | 'integer'
  | 'number'
  | 'boolean'
  | 'enum'
  | 'stringArray'
  // "format": "sard-secret" (ADR 0027): the name of one of the host's secrets, never a value.
  | 'secret'

export interface FieldModel {
  name: string
  kind: FieldKind
  property: Json
  required: boolean
  enumValues: string[]
}

export type FormModel = { kind: 'form'; fields: FieldModel[] } | { kind: 'json' }

export type FieldValue = string | boolean | string[]
export type FormValues = Record<string, FieldValue>

function isObject(value: unknown): value is Json {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function arrayKind(property: Json): FieldKind | null {
  return isObject(property.items) && property.items.type === 'string' ? 'stringArray' : null
}

function stringKind(property: Json): FieldKind {
  if (property.format === 'sard-secret') return 'secret'
  return Array.isArray(property.enum) ? 'enum' : 'string'
}

function kindOf(property: Json): FieldKind | null {
  switch (property.type) {
    case 'array':
      return arrayKind(property)
    case 'string':
      return stringKind(property)
    case 'integer':
    case 'number':
    case 'boolean':
      return property.type
    default:
      return null
  }
}

function fieldOf(name: string, property: unknown, required: string[]): FieldModel | null {
  if (!isObject(property)) return null
  const kind = kindOf(property)
  if (kind === null) return null
  const values = Array.isArray(property.enum) ? property.enum.map(String) : []
  return { name, kind, property, required: required.includes(name), enumValues: values }
}

// A form for the supported subset (object of strings, numbers, booleans, enums, secret
// names and arrays of strings); a JSON editor of the whole config for anything else.
export function buildFormModel(schema: Json): FormModel {
  if (schema.type !== 'object' || !isObject(schema.properties)) return { kind: 'json' }
  const required = Array.isArray(schema.required) ? schema.required.map(String) : []
  const fields = Object.entries(schema.properties).map(([name, property]) =>
    fieldOf(name, property, required),
  )
  return fields.every((field) => field !== null) ? { kind: 'form', fields } : { kind: 'json' }
}

export interface FieldText {
  title: string
  description: string | null
  // The first example, as a hint under the field.
  example: string | null
}

function translated(property: Json, language: string, key: 'title' | 'description') {
  const i18n = property['x-sard-i18n']
  if (!isObject(i18n) || !Object.hasOwn(i18n, language)) return null
  const entry = i18n[language]
  const text = isObject(entry) ? entry[key] : undefined
  return typeof text === 'string' && text !== '' ? text : null
}

function plain(property: Json, key: 'title' | 'description') {
  const text = property[key]
  return typeof text === 'string' && text !== '' ? text : null
}

function exampleOf(property: Json): string | null {
  if (!Array.isArray(property.examples) || property.examples.length === 0) return null
  const [first] = property.examples as unknown[]
  return Array.isArray(first) ? first.join(', ') : String(first)
}

// The title and description in the interface language when the schema has them
// in x-sard-i18n, the schema's own otherwise; a property without a title is its name.
export function fieldText(name: string, property: Json, language: string): FieldText {
  return {
    title: translated(property, language, 'title') ?? plain(property, 'title') ?? name,
    description: translated(property, language, 'description') ?? plain(property, 'description'),
    example: exampleOf(property),
  }
}

function initialValue(field: FieldModel): FieldValue {
  if (field.kind === 'boolean') return field.property.default === true
  return field.kind === 'stringArray' ? [] : ''
}

export function initialValues(model: FormModel & { kind: 'form' }): FormValues {
  return Object.fromEntries(model.fields.map((field) => [field.name, initialValue(field)]))
}

function storedValue(field: FieldModel, stored: unknown): FieldValue | null {
  switch (field.kind) {
    case 'boolean':
      return typeof stored === 'boolean' ? stored : null
    case 'stringArray':
      return Array.isArray(stored) ? stored.map(String) : null
    default:
      return typeof stored === 'string' || typeof stored === 'number' ? String(stored) : null
  }
}

function valueFrom(field: FieldModel, stored: unknown): FieldValue {
  return storedValue(field, stored) ?? initialValue(field)
}

// The form of an existing source: its config where it fits the field, the field's start otherwise.
export function valuesFromConfig(model: FormModel & { kind: 'form' }, config: Json): FormValues {
  return Object.fromEntries(
    model.fields.map((field) => [field.name, valueFrom(field, config[field.name])]),
  )
}

function typed(field: FieldModel, value: string): string | number {
  if (field.kind !== 'integer' && field.kind !== 'number') return value
  const number = Number(value)
  return Number.isNaN(number) ? value : number
}

function isEmpty(value: FieldValue): boolean {
  return Array.isArray(value) ? value.length === 0 : value === ''
}

// The config to send: empty optional fields do not go, a switch always does (П4);
// an empty required field goes missing too, and the server says so.
export function buildConfig(model: FormModel & { kind: 'form' }, values: FormValues): Json {
  const config: Json = {}
  for (const field of model.fields) {
    const value = values[field.name]
    if (typeof value === 'boolean' || Array.isArray(value)) {
      if (typeof value === 'boolean' || !isEmpty(value)) config[field.name] = value
    } else if (!isEmpty(value)) {
      config[field.name] = typed(field, value)
    }
  }
  return config
}

// The text of the JSON editor as a config; false when it is not a JSON object (nothing to send).
export function parseConfigJson(text: string): { ok: true; config: Json } | { ok: false } {
  try {
    const value: unknown = JSON.parse(text)
    return isObject(value) ? { ok: true, config: value } : { ok: false }
  } catch {
    return { ok: false }
  }
}

export function configToJson(config: Json): string {
  return JSON.stringify(config, null, 2)
}
