// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// Generates src/routeTree.gen.ts from src/routes with the same generator and
// tsr.config.json the Vite plugin uses, without running tsc or a build first.
import { Generator, getConfig } from '@tanstack/router-generator'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('..', import.meta.url))
await new Generator({ config: getConfig({}, root), root }).run()
