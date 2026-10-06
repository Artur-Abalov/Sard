// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

export const DEFAULT_VERSION = 'dev'

const PLACEHOLDER = '%SARD_VERSION%'

/** The version a build is stamped with: SARD_VERSION, or dev when it is unset or blank. */
export function versionOf(env: string | undefined): string {
  return env?.trim() || DEFAULT_VERSION
}

function escapeAttribute(value: string): string {
  return value
    .replaceAll('&', '&amp;')
    .replaceAll('"', '&quot;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
}

/** index.html with its version placeholder replaced; [version] is escaped for an HTML attribute. */
export function stampVersion(html: string, version: string): string {
  return html.replaceAll(PLACEHOLDER, () => escapeAttribute(version))
}
