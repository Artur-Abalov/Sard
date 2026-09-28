// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { setupWorker } from 'msw/browser'
import { handlers } from './handlers'
import { resetMockState } from './state'

const worker = setupWorker(...handlers)

// Starts the service worker. An API request without a handler is reported as
// an error in the console; everything else (Vite modules, assets) passes through.
// The dev worker starts signed out (К4): the login page opens first, as it would
// against the real server.
export function startMocks() {
  resetMockState({ signedIn: false })
  return worker.start({
    onUnhandledRequest(request, print) {
      if (new URL(request.url).pathname.startsWith('/api/')) print.error()
    },
  })
}
