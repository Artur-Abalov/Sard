// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Badge, Box, Button, Group, Loader, Stack, Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { codeBlock, logLevelColors, tabularNums, tones } from '../theme'
import { EMPTY } from '../format'
import { useFormat } from '../useFormat'
import { ErrorBlock } from './ErrorBlock'
import { Mono } from './Mono'
import { useStepLog } from './useStepLog'

// The log of one step as the server has it: lines in seq order with their time and level, the text
// untouched (secrets are masked by the agent, markup is shown as text). A line the server wrote
// itself, the truncation mark, has no time.
export function StepLogView({
  runId,
  stepId,
  active,
}: {
  runId: string
  stepId: string
  active: boolean
}) {
  const { t } = useTranslation()
  const format = useFormat()
  const { log, more } = useStepLog(runId, stepId, active)
  if (!log.loaded) {
    return log.error === null ? (
      <Loader size="sm" aria-label={t('common.loading')} />
    ) : (
      <ErrorBlock error={log.error} onRetry={() => void more()} />
    )
  }
  return (
    <Stack gap="xs">
      {log.truncated && <Alert color={tones.warning}>{t('run.logTruncated')}</Alert>}
      {log.error !== null && <ErrorBlock error={log.error} onRetry={() => void more()} />}
      {log.lines.length === 0 ? (
        <Text c="dimmed">{t('run.noLogLines')}</Text>
      ) : (
        <Box style={codeBlock} p="xs">
          <Stack gap={2} w="max-content" miw="100%">
            {log.lines.map((line) => (
              <Group key={line.seq} gap="sm" wrap="nowrap" align="flex-start">
                <Mono w={190} style={{ ...tabularNums, whiteSpace: 'nowrap', flexShrink: 0 }}>
                  {line.time === null ? EMPTY : format.time(line.time)}
                </Mono>
                <Box w={140} style={{ flexShrink: 0 }}>
                  <Badge color={logLevelColors[line.level]}>
                    {t(`enum.LogLevel.${line.level}`)}
                  </Badge>
                </Box>
                <Mono style={{ ...tabularNums, whiteSpace: 'pre' }}>{line.text}</Mono>
              </Group>
            ))}
          </Stack>
        </Box>
      )}
      {log.hasMore && (
        <Group>
          <Button variant="default" size="xs" onClick={() => void more()}>
            {t('common.showMore')}
          </Button>
        </Group>
      )}
    </Stack>
  )
}
