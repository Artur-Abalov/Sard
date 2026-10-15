// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { useTranslation } from 'react-i18next'
import type { components } from '../api/schema'

// An agent's role as text, in the language of the console.
export function useRoleLabel(): (role: components['schemas']['AgentRole']) => string {
  const { t } = useTranslation()
  return (role) => t(`enum.AgentRole.${role}`)
}
