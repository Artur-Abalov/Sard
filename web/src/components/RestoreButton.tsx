// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import type { components } from '../api/schema'
import { ButtonLink } from './links'

// The way to the restore page of a snapshot, or the server's reason why there is none.
export function RestoreButton({
  sourceId,
  snapshotId,
  blockedReason = null,
  restorable,
}: {
  sourceId: string
  snapshotId: string
  restorable: boolean
  blockedReason?: components['schemas']['RestoreBlockedReason'] | null
}) {
  const { t } = useTranslation()
  if (!restorable) {
    return blockedReason === null ? null : (
      <Text size="sm" c="dimmed">
        {t(`restore.blocked.${blockedReason}`)}
      </Text>
    )
  }
  return (
    <ButtonLink
      size="xs"
      variant="default"
      to="/sources/$sourceId/snapshots/$snapshotId/restore"
      params={{ sourceId, snapshotId }}
      search={{}}
    >
      {t('restore.button')}
    </ButtonLink>
  )
}
