// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge, Group, Stack, Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { stepMarks, type Onboarding } from '../../onboarding/state'
import { tones } from '../../theme'

const badgeTone = {
  done: tones.success,
  current: tones.info,
  todo: tones.neutral,
  soon: tones.neutral,
} as const

// The four steps in the server's order. The F4b steps are announced only: no action and
// no sign of being required (Рк5).
export function StepList({ onboarding }: { onboarding: Onboarding }) {
  const { t } = useTranslation()
  return (
    <Stack gap="xs" component="ol" aria-label={t('setup.title')} p={0} m={0}>
      {stepMarks(onboarding).map(({ id, mark }) => (
        <Group key={id} component="li" gap="sm" style={{ listStyle: 'none' }}>
          <Badge variant="light" color={badgeTone[mark]}>
            {t(`setup.marks.${mark}`)}
          </Badge>
          <Text fw={mark === 'current' ? 600 : 400}>{t(`setup.steps.${id}`)}</Text>
        </Group>
      ))}
    </Stack>
  )
}
