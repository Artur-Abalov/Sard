// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { setupWorker } from 'msw/browser'
import { handlers } from './handlers'
import { resetMockState } from './state'

const worker = setupWorker(...handlers)

// Starts the service worker. An API request without a handler is reported as
// an error in the console; everything else (Vite modules, assets) passes through.
// The dev worker starts signed out (К4): the login page opens first, as it would
// against the real server. With VITE_MOCK_ONBOARDING=1 it starts a clean installation:
// the wizard opens first and takes the code of the mocks (MOCK_SETUP_CODE).
export function startMocks() {
  resetMockState({ signedIn: false, freshInstall: import.meta.env.VITE_MOCK_ONBOARDING === '1' })
  return worker.start({
    onUnhandledRequest(request, print) {
      if (new URL(request.url).pathname.startsWith('/api/')) print.error()
    },
  })
}
