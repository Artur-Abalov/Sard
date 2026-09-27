// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Outlet } from '@tanstack/react-router'
import { lazy, Suspense } from 'react'

// Devtools load only in development; in a production build the branch is
// statically false and the import is dropped from the bundle.
const Devtools = import.meta.env.DEV ? lazy(() => import('./Devtools')) : () => null

export function Root() {
  return (
    <>
      <Outlet />
      <Suspense>
        <Devtools />
      </Suspense>
    </>
  )
}
