// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { afterAll, afterEach, vi } from 'vitest'
import { server } from './node'

// The page the app would run on: the API client and relative handler paths
// resolve against it, as they do in the browser.
vi.stubGlobal('location', new URL('http://localhost:5173/'))

// Every test talks to the mock server only: a request no handler covers fails
// the test instead of going to the network. listen() runs here, before the test
// file's imports, because openapi-fetch keeps the fetch it finds at creation.
server.listen({ onUnhandledRequest: 'error' })
afterEach(() => server.resetHandlers())
afterAll(() => server.close())
