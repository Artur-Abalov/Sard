// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Badge, Card, Group, Stack, Table, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { verifiersQuery } from '../api/queries'
import type { components } from '../api/schema'
import { AgentName } from '../components/AgentName'
import { Loaded } from '../components/Loaded'
import { Mono } from '../components/Mono'
import { VerifierInstruction } from '../components/VerifierInstruction'
import { tones } from '../theme'
import { useFormat } from '../useFormat'

type Verifier = components['schemas']['Verifier']

function Row({ verifier }: { verifier: Verifier }) {
  const { t } = useTranslation()
  const format = useFormat()
  return (
    <Table.Tr>
      <Table.Td>
        <AgentName agentId={verifier.agentId} />
      </Table.Td>
      <Table.Td>
        <Badge color={verifier.online ? tones.success : tones.neutral}>
          {verifier.online
            ? t('verification.verifiers.online')
            : t('verification.verifiers.offline')}
        </Badge>
      </Table.Td>
      <Table.Td>
        <Group gap={4}>
          {verifier.repositoryIds.length === 0 && <Text c="dimmed">—</Text>}
          {verifier.repositoryIds.map((id) => (
            <Mono key={id}>{id}</Mono>
          ))}
        </Group>
      </Table.Td>
      <Table.Td>
        <Mono>
          {verifier.freeBytes === null
            ? t('verification.verifiers.unknown')
            : format.bytes(verifier.freeBytes)}
        </Mono>
      </Table.Td>
      <Table.Td>
        <Mono>
          {verifier.sandbox === null
            ? t('verification.verifiers.unknown')
            : [verifier.sandbox, verifier.sandboxVersion].filter((part) => part !== null).join(' ')}
        </Mono>
      </Table.Td>
      <Table.Td ta="right">
        <Mono>{verifier.verificationsDone}</Mono>
      </Table.Td>
      <Table.Td ta="right">
        <Mono c={verifier.infrastructureFailures > 0 ? tones.warning : undefined}>
          {verifier.infrastructureFailures}
        </Mono>
      </Table.Td>
    </Table.Tr>
  )
}

// The verifiers of the installation (rule 11): who is online, which repositories they hold a key to, their free
// space and sandbox, how many checks they did in 30 days and how many failed by their own fault.
export function Verifiers() {
  const { t } = useTranslation()
  const verifiers = useQuery(verifiersQuery())
  return (
    <Stack>
      <Title order={2}>{t('verification.verifiers.title')}</Title>
      <Loaded query={verifiers}>
        {(list) =>
          list.length === 0 ? (
            <>
              <Text c="dimmed">{t('verification.verifiers.none')}</Text>
              <VerifierInstruction open />
            </>
          ) : (
            <Card withBorder>
              <Table>
                <Table.Thead>
                  <Table.Tr>
                    <Table.Th>{t('verification.verifiers.host')}</Table.Th>
                    <Table.Th>{t('verification.verifiers.state')}</Table.Th>
                    <Table.Th>{t('verification.verifiers.repositories')}</Table.Th>
                    <Table.Th>{t('verification.verifiers.freeSpace')}</Table.Th>
                    <Table.Th>{t('verification.verifiers.sandbox')}</Table.Th>
                    <Table.Th ta="right">{t('verification.verifiers.done')}</Table.Th>
                    <Table.Th ta="right">{t('verification.verifiers.ownFailures')}</Table.Th>
                  </Table.Tr>
                </Table.Thead>
                <Table.Tbody>
                  {list.map((verifier) => (
                    <Row key={verifier.agentId} verifier={verifier} />
                  ))}
                </Table.Tbody>
              </Table>
            </Card>
          )
        }
      </Loaded>
    </Stack>
  )
}
