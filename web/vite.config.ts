// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { tanstackRouter } from '@tanstack/router-plugin/vite'
import react from '@vitejs/plugin-react'
import { createReadStream } from 'node:fs'
import { createRequire } from 'node:module'
import type { Plugin } from 'vite'
import { defineConfig } from 'vitest/config'

// The dev server proxies API calls to a locally running sard-server.
const server = process.env.SARD_SERVER_URL ?? 'http://localhost:8080'

// Serves MSW's service worker from node_modules in the dev server only: it is not
// copied into public/ and so never reaches a production build (src/mocks/README.md).
function mockServiceWorker(): Plugin {
  const file = createRequire(import.meta.url).resolve('msw/mockServiceWorker.js')
  return {
    name: 'sard:mock-service-worker',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use('/mockServiceWorker.js', (_req, res) => {
        res.setHeader('Content-Type', 'text/javascript')
        createReadStream(file).pipe(res)
      })
    },
  }
}

export default defineConfig({
  plugins: [
    // Generates src/routes -> src/routeTree.gen.ts (settings in tsr.config.json);
    // must run before the React plugin.
    tanstackRouter({ target: 'react' }),
    react(),
    mockServiceWorker(),
  ],
  server: {
    proxy: {
      '/api': server,
      '/actuator': server,
      '/v3/api-docs': server,
    },
  },
  test: {
    include: ['src/**/*.test.ts'],
    environment: 'node',
    setupFiles: ['src/mocks/vitest.setup.ts'],
  },
})
