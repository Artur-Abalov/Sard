// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Badge, Button, Group, Loader, Stack, Table, Text } from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { logLevelColors } from '../theme'
import { EMPTY } from '../format'
import { useFormat } from '../useFormat'
import { ErrorBlock } from './ErrorBlock'
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
      {log.truncated && <Alert color="yellow">{t('run.logTruncated')}</Alert>}
      {log.error !== null && <ErrorBlock error={log.error} onRetry={() => void more()} />}
      {log.lines.length === 0 ? (
        <Text c="dimmed">{t('run.noLogLines')}</Text>
      ) : (
        <Table withRowBorders={false} verticalSpacing={2}>
          <Table.Tbody>
            {log.lines.map((line) => (
              <Table.Tr key={line.seq}>
                <Table.Td w={170}>{line.time === null ? EMPTY : format.time(line.time)}</Table.Td>
                <Table.Td w={90}>
                  <Badge color={logLevelColors[line.level]} variant="light">
                    {t(`enum.LogLevel.${line.level}`)}
                  </Badge>
                </Table.Td>
                <Table.Td>
                  <Text span ff="monospace" size="sm" style={{ whiteSpace: 'pre-wrap' }}>
                    {line.text}
                  </Text>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
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
