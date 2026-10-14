// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

interface ImportMetaEnv {
  // '1' serves API responses from src/mocks in the dev server (MSW service worker).
  readonly VITE_API_MOCKS?: string
  // '1' together with VITE_API_MOCKS starts the mocks as a clean installation (the first-start wizard);
  // 'upgraded' starts an upgraded installation: only the issue of tokens is refused until the wizard is done.
  readonly VITE_MOCK_ONBOARDING?: string
  // '1': the first run of the database in "back up now" fails once, with a message of the step.
  readonly VITE_MOCK_SELF_BACKUP_FAIL?: string
}
