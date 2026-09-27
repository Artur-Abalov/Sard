// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import '@mantine/core/styles.css'
import './i18n'
import { MantineProvider } from '@mantine/core'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createAppRouter } from './router'
import { defaultColorScheme, theme } from './theme'

const queryClient = new QueryClient()
const router = createAppRouter({ queryClient })

// VITE_API_MOCKS=1 npm run dev: API responses come from src/mocks. The check is
// statically false in a production build, so MSW is not bundled.
async function enableMocks() {
  if (import.meta.env.DEV && import.meta.env.VITE_API_MOCKS === '1') {
    const { startMocks } = await import('./mocks/browser')
    await startMocks()
  }
}

// The first requests must already see the mocks, so rendering waits for the worker.
void enableMocks().then(() =>
  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <MantineProvider theme={theme} defaultColorScheme={defaultColorScheme}>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </MantineProvider>
    </StrictMode>,
  ),
)
