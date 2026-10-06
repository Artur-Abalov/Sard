// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Anchor, Stack } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { tones } from '../theme'

// The server hands out no packages, so there are no commands: the reason and the manual installation.
export function DownloadsOff({ manualInstallDoc }: { manualInstallDoc: string }) {
  const { t } = useTranslation()
  return (
    <Alert color={tones.notice}>
      <Stack gap="xs" align="flex-start">
        {t('install.off')}
        <Anchor href={manualInstallDoc} target="_blank" rel="noopener noreferrer">
          {t('install.manual')}
        </Anchor>
      </Stack>
    </Alert>
  )
}
