// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Where in a form an error of the server belongs.
export type FieldTarget =
  | { kind: 'name' | 'agent' | 'plugin' | 'repository' | 'ttl' | 'label' }
  // The config as a whole: the JSON editor, or a field the form has none for.
  | { kind: 'config' }
  | { kind: 'configField'; path: string[] }
  // The form as a whole.
  | { kind: 'form' }

const TOP_LEVEL: Record<string, FieldTarget> = {
  name: { kind: 'name' },
  agentId: { kind: 'agent' },
  plugin: { kind: 'plugin' },
  repositoryName: { kind: 'repository' },
  config: { kind: 'config' },
  ttlSeconds: { kind: 'ttl' },
  label: { kind: 'label' },
}

// RFC 6901: "~1" is "/", then "~0" is "~", in that order.
function unescapePointer(segment: string): string {
  return segment.replaceAll('~1', '/').replaceAll('~0', '~')
}

// `field` is a JSON Pointer without the leading slash ("config/paths/0");
// `configFields` are the top-level properties the config form has.
export function mapField(field: string, configFields: readonly string[]): FieldTarget {
  const [head, ...rest] = field.split('/')
  if (head !== 'config' || rest.length === 0) {
    return Object.hasOwn(TOP_LEVEL, head) ? TOP_LEVEL[head] : { kind: 'form' }
  }
  const path = rest.map(unescapePointer)
  return configFields.includes(path[0]) ? { kind: 'configField', path } : { kind: 'config' }
}
