// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { describe, expect, test } from 'vitest'
import filesSchema from '../../agent/plugins/files/schema.json'
import {
  buildConfig,
  buildFormModel,
  configToJson,
  fieldText,
  initialValues,
  parseConfigJson,
  valuesFromConfig,
  type FormModel,
} from './schemaForm'

function formOf(schema: Record<string, unknown>) {
  const model = buildFormModel(schema)
  if (model.kind !== 'form') throw new Error('expected a form')
  return model
}

describe('the text of a schema property', () => {
  const property = (i18n: unknown) => ({
    title: 'Paths',
    description: 'Absolute paths',
    'x-sard-i18n': i18n,
  })
  const ru = { ru: { title: 'Пути', description: 'Пути на хосте' } }

  test.each([
    ['ru with both', ru, 'ru', 'Пути', 'Пути на хосте'],
    ['ru with both, language en', ru, 'en', 'Paths', 'Absolute paths'],
    ['ru with a title only', { ru: { title: 'Пути' } }, 'ru', 'Пути', 'Absolute paths'],
    ['none', undefined, 'ru', 'Paths', 'Absolute paths'],
    ['an empty title', { ru: { title: '', description: '' } }, 'ru', 'Paths', 'Absolute paths'],
    ['a string instead of an object', 'ru', 'ru', 'Paths', 'Absolute paths'],
    ['a language that is not an own key', {}, 'constructor', 'Paths', 'Absolute paths'],
  ])('%s', (_name, i18n, language, title, description) => {
    expect(fieldText('paths', property(i18n), language)).toMatchObject({ title, description })
  })

  test('a property without a title is labelled with its name', () => {
    expect(fieldText('one_file_system', { type: 'boolean' }, 'ru').title).toBe('one_file_system')
  })

  test('the first example is a hint', () => {
    expect(fieldText('paths', { examples: [['/etc', '/var/www']] }, 'en').example).toBe(
      '/etc, /var/www',
    )
    expect(fieldText('x', { examples: ['abc'] }, 'en').example).toBe('abc')
    expect(fieldText('x', { examples: [true] }, 'en').example).toBe('true')
    expect(fieldText('x', {}, 'en').example).toBeNull()
  })
})

describe('the form model of a config schema', () => {
  test('the files plugin: paths is required, exclude is not, a switch starts at its default', () => {
    const model = formOf(filesSchema)

    expect(model.fields.map((f) => [f.name, f.kind, f.required])).toEqual([
      ['paths', 'stringArray', true],
      ['exclude', 'stringArray', false],
      ['one_file_system', 'boolean', false],
    ])
    expect(initialValues(model)).toEqual({ paths: [], exclude: [], one_file_system: false })
  })

  test('every supported type has its field kind', () => {
    const model = formOf({
      type: 'object',
      properties: {
        a: { type: 'string' },
        b: { type: 'integer' },
        c: { type: 'number' },
        d: { type: 'boolean', default: true },
        e: { type: 'string', enum: ['x', 'y'] },
        f: { type: 'array', items: { type: 'string' } },
        g: { type: 'string', format: 'sard-secret' },
      },
    })

    expect(model.fields.map((f) => f.kind)).toEqual([
      'string',
      'integer',
      'number',
      'boolean',
      'enum',
      'stringArray',
      'secret',
    ])
    expect(model.fields[4].enumValues).toEqual(['x', 'y'])
    expect(initialValues(model)).toMatchObject({ a: '', d: true, e: '', g: '' })
  })

  test.each([
    ['a nested object', { type: 'object', properties: { a: { type: 'object' } } }],
    ['oneOf', { type: 'object', properties: { a: { oneOf: [{ type: 'string' }] } } }],
    ['a $ref', { type: 'object', properties: { a: { $ref: '#/$defs/a' } } }],
    [
      'an array of numbers',
      { type: 'object', properties: { a: { type: 'array', items: { type: 'number' } } } },
    ],
    ['a top level that is not an object', { type: 'array' }],
    ['a top-level oneOf', { oneOf: [{ type: 'object' }] }],
  ])('%s gives a JSON editor of the whole config', (_name, schema) => {
    expect(buildFormModel(schema)).toEqual({ kind: 'json' })
  })

  test('a schema that is nothing like one gives a JSON editor too', () => {
    expect(buildFormModel({})).toEqual({ kind: 'json' })
  })
})

describe('the config built from the form', () => {
  const model: FormModel = formOf(filesSchema)

  test.each([
    [
      '/etc, nothing excluded, the switch off',
      { paths: ['/etc'], exclude: [], one_file_system: false },
      { paths: ['/etc'], one_file_system: false },
    ],
    [
      'no paths, one exclusion',
      { paths: [], exclude: ['*.log'], one_file_system: false },
      { exclude: ['*.log'], one_file_system: false },
    ],
    [
      'an empty second path is kept',
      { paths: ['/etc', ''], exclude: [], one_file_system: false },
      { paths: ['/etc', ''], one_file_system: false },
    ],
  ])('%s', (_name, values, config) => {
    expect(buildConfig(model, values)).toEqual(config)
  })

  test('empty optional text, numbers and choices are left out, filled ones typed', () => {
    const typed = formOf({
      type: 'object',
      properties: {
        s: { type: 'string' },
        i: { type: 'integer' },
        n: { type: 'number' },
        e: { type: 'string', enum: ['x'] },
        k: { type: 'string', format: 'sard-secret' },
      },
    })

    expect(buildConfig(typed, { s: '', i: '', n: '', e: '', k: '' })).toEqual({})
    expect(buildConfig(typed, { s: 'v', i: '7', n: '1.5', e: 'x', k: 'db-password' })).toEqual({
      s: 'v',
      i: 7,
      n: 1.5,
      e: 'x',
      k: 'db-password',
    })
  })

  test('a number that is not one goes as text, for the server to refuse', () => {
    const typed = formOf({ type: 'object', properties: { i: { type: 'integer' } } })

    expect(buildConfig(typed, { i: 'abc' })).toEqual({ i: 'abc' })
  })

  test('nothing of the schema itself gets into the config', () => {
    const config = buildConfig(model, initialValues(model))

    expect(JSON.stringify(config)).not.toMatch(/x-sard-i18n|title|description|examples/)
  })
})

describe('the form of an existing source', () => {
  const model = formOf(filesSchema)

  test('is filled with its config, defaults where the config has none', () => {
    expect(valuesFromConfig(model, { paths: ['/etc'] })).toEqual({
      paths: ['/etc'],
      exclude: [],
      one_file_system: false,
    })
  })

  test('a value of another type than the field takes is shown as the field starts', () => {
    expect(valuesFromConfig(model, { paths: 'x', one_file_system: 'yes' })).toEqual(
      initialValues(model),
    )
  })

  test('numbers are shown as text', () => {
    const typed = formOf({ type: 'object', properties: { i: { type: 'integer' } } })

    expect(valuesFromConfig(typed, { i: 7 })).toEqual({ i: '7' })
  })
})

describe('the JSON editor of a whole config', () => {
  test('text of a JSON object is the config', () => {
    expect(parseConfigJson('{"paths":["/etc"]}')).toEqual({ ok: true, config: { paths: ['/etc'] } })
  })

  test.each(['', '{', '[1]', '"x"', 'null', '7'])('%j cannot be sent', (text) => {
    expect(parseConfigJson(text)).toEqual({ ok: false })
  })

  test('an existing config is shown as indented JSON', () => {
    expect(configToJson({ paths: ['/etc'] })).toBe('{\n  "paths": [\n    "/etc"\n  ]\n}')
  })
})
