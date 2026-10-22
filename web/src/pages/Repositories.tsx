// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Stack, Table, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { repositoriesQuery } from '../api/queries'
import type { components } from '../api/schema'
import { CopyBox } from '../components/CopyBox'
import { EmptyState } from '../components/EmptyState'
import { Loaded } from '../components/Loaded'
import { Mono } from '../components/Mono'
import { RecoveryKeyMark } from '../components/RecoveryKeyMark'
import { useRoleLabel } from '../components/useRoleLabel'

function Holders({ holders }: { holders: components['schemas']['RepositoryHolderInfo'][] }) {
  const roleLabel = useRoleLabel()
  return (
    <Stack gap={0}>
      {holders.map((holder) => (
        <span key={holder.agentId}>
          <Mono>{holder.hostname}</Mono> ({roleLabel(holder.role)}){' '}
          <Mono>{holder.repositoryName}</Mono>
        </span>
      ))}
    </Stack>
  )
}

function RepositoryRow({ item }: { item: components['schemas']['RepositoryInfo'] }) {
  const { t } = useTranslation()
  return (
    <Table.Tr>
      <Table.Td>
        <Mono>{item.repositoryId}</Mono>
      </Table.Td>
      <Table.Td>
        <Holders holders={item.holders} />
      </Table.Td>
      <Table.Td>
        <RecoveryKeyMark flag={item.recoveryKey} />
        {item.recoveryKeyCommand !== null && (
          <CopyBox value={item.recoveryKeyCommand} label={t('repositories.command')} />
        )}
      </Table.Td>
    </Table.Tr>
  )
}

// The repositories the agents report and whether each has a recovery key (V3, КВ4). The server decides the flag
// and writes the command; the page shows them.
export function Repositories() {
  const { t } = useTranslation()
  const query = useQuery(repositoriesQuery())
  return (
    <Stack>
      <Title order={2}>{t('repositories.title')}</Title>
      <Loaded query={query}>
        {(list) =>
          list.items.length === 0 ? (
            <EmptyState text={t('repositories.empty')} />
          ) : (
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t('repositories.repositoryId')}</Table.Th>
                  <Table.Th>{t('repositories.holders')}</Table.Th>
                  <Table.Th>{t('repositories.recoveryKey.label')}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {list.items.map((item) => (
                  <RepositoryRow key={item.repositoryId} item={item} />
                ))}
              </Table.Tbody>
            </Table>
          )
        }
      </Loaded>
    </Stack>
  )
}
