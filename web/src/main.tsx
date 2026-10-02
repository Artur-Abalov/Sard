// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import '@fontsource/ibm-plex-sans/cyrillic-400.css'
import '@fontsource/ibm-plex-sans/cyrillic-600.css'
import '@fontsource/ibm-plex-sans/latin-400.css'
import '@fontsource/ibm-plex-sans/latin-600.css'
import '@fontsource/jetbrains-mono/cyrillic-400.css'
import '@fontsource/jetbrains-mono/latin-400.css'
import '@mantine/core/styles.css'
import './i18n'
import { MantineProvider } from '@mantine/core'
import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createAppQueryClient } from './auth/queryClient'
import { createAppRouter } from './router'
import { cssVariablesResolver, defaultColorScheme, reducedMotionCss, theme } from './theme'

// A 401 from any query, anywhere in the app, clears the cache and goes to sign-in
// with the current page remembered (rule "Ответ 401 во время работы ведёт на вход").
function handleUnauthenticated() {
  const { pathname, searchStr } = router.state.location
  if (pathname === '/login') return
  void router.navigate({ to: '/login', search: { redirect: pathname + searchStr } })
}

const queryClient = createAppQueryClient(() => handleUnauthenticated())
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
      <MantineProvider
        theme={theme}
        cssVariablesResolver={cssVariablesResolver}
        defaultColorScheme={defaultColorScheme}
      >
        <style>{reducedMotionCss}</style>
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      </MantineProvider>
    </StrictMode>,
  ),
)
