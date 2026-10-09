// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Button, Group, Modal, Stack } from '@mantine/core'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { tones } from '../theme'

// A confirmation that names its consequences. Nothing is sent until the confirm button is
// pressed; Escape and Cancel close it (Mantine returns the focus to the opening button).
export function ConfirmModal({
  opened,
  title,
  confirmLabel,
  busy,
  disabled = false,
  onConfirm,
  onClose,
  children,
}: {
  opened: boolean
  title: string
  confirmLabel: string
  busy: boolean
  // The confirm button stays off until the dialog's own condition holds.
  disabled?: boolean
  onConfirm: () => void
  onClose: () => void
  children: ReactNode
}) {
  const { t } = useTranslation()
  return (
    <Modal opened={opened} onClose={onClose} title={title}>
      <Stack>
        {children}
        <Group justify="flex-end">
          <Button variant="default" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button
            variant="default"
            c={tones.error}
            loading={busy}
            disabled={disabled}
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
