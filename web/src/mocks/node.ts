// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { setupServer } from 'msw/node'
import { handlers } from './handlers'

export const server = setupServer(...handlers)
