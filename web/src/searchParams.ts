// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// The router's reading and writing of the address query. Plain `name=value` pairs, a name
// given several times is a list: the filter of the runs page reads `?status=failed&status=running`
// and the address stays that way, instead of being rewritten as JSON.

export function parseSearch(searchStr: string): Record<string, string | string[]> {
  const result: Record<string, string | string[]> = {}
  for (const [name, value] of new URLSearchParams(searchStr)) {
    const seen = result[name]
    if (seen === undefined) {
      result[name] = value
    } else {
      result[name] = Array.isArray(seen) ? [...seen, value] : [seen, value]
    }
  }
  return result
}

export function stringifySearch(search: Record<string, unknown>): string {
  const params = new URLSearchParams()
  for (const [name, value] of Object.entries(search)) {
    if (value === undefined) continue
    for (const item of Array.isArray(value) ? value : [value]) {
      params.append(name, String(item))
    }
  }
  const text = params.toString()
  return text === '' ? '' : `?${text}`
}
