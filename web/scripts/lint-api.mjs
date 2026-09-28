// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Lints src/api/openapi.json with Redocly's rules (redocly.yaml). Exits
// non-zero on any error or warning.
import { formatProblems, getTotals, lint, loadConfig } from '@redocly/openapi-core'

const config = await loadConfig({
  configPath: new URL('../redocly.yaml', import.meta.url).pathname,
})
const problems = await lint({
  ref: new URL('../src/api/openapi.json', import.meta.url).pathname,
  config,
})
const totals = getTotals(problems)
formatProblems(problems, { format: 'stylish', totals, version: 'openapi-core' })
if (problems.length === 0) console.log('src/api/openapi.json: no problems')
process.exit(totals.errors + totals.warnings > 0 ? 1 : 0)
