// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

interface ImportMetaEnv {
  // '1' serves API responses from src/mocks in the dev server (MSW service worker).
  readonly VITE_API_MOCKS?: string
  // '1' together with VITE_API_MOCKS starts the mocks as a clean installation (the first-start wizard).
  readonly VITE_MOCK_ONBOARDING?: string
}
