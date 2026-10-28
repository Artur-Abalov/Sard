// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Button, Card, List, Stack, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { verificationSetupQuery } from '../api/queries'
import { CopyBox } from './CopyBox'
import { AppLink, ButtonLink } from './links'
import { Loaded } from './Loaded'
import { Mono } from './Mono'

function Steps() {
  const { t } = useTranslation()
  const setup = useQuery(verificationSetupQuery())
  return (
    <Loaded query={setup}>
      {(data) => (
        <List type="ordered" spacing="md">
          <List.Item>
            <Stack gap="xs" align="flex-start">
              <Text>{t('verification.instruction.token')}</Text>
              <ButtonLink
                to="/tokens"
                search={{
                  create: true,
                  role: data.tokenRole === 'verifier' ? 'verifier' : undefined,
                }}
                size="xs"
              >
                {t('verification.instruction.issueToken')}
              </ButtonLink>
            </Stack>
          </List.Item>
          <List.Item>
            <Text>{t('verification.instruction.install')}</Text>
          </List.Item>
          <List.Item>
            <Stack gap="xs">
              <Text>{t('verification.instruction.keys')}</Text>
              {data.repositories.length === 0 && (
                <Text c="dimmed">{t('verification.instruction.noRepositories')}</Text>
              )}
              {data.repositories.map((entry) => (
                <Stack key={`${entry.sourceAgentId} ${entry.repositoryName}`} gap="xs">
                  <Text>
                    <AppLink to="/agents/$agentId" params={{ agentId: entry.sourceAgentId }}>
                      <Mono>{entry.sourceHostname}</Mono>
                    </AppLink>{' '}
                    <Mono>{entry.repositoryName}</Mono>
                  </Text>
                  {entry.keyAddCommand === null ? (
                    <Text c="dimmed">{t('verifierSetup.unsafeName')}</Text>
                  ) : (
                    <CopyBox value={entry.keyAddCommand} label={t('verifierSetup.keyAddHint')} />
                  )}
                </Stack>
              ))}
            </Stack>
          </List.Item>
          <List.Item>
            <Stack gap="xs">
              <Text>{t('verification.instruction.repoAdd')}</Text>
              <CopyBox value={data.verifierRepoAdd} label={t('verifierSetup.repoAddHint')} />
              <Text size="sm" c="dimmed">
                {t('verification.instruction.readOnly')}
              </Text>
            </Stack>
          </List.Item>
        </List>
      )}
    </Loaded>
  )
}

// What to do to get restore verification going: the steps with the commands the server builds. Nothing here
// takes a password: they are typed on the hosts only.
export function VerifierInstruction({ open = false }: { open?: boolean }) {
  const { t } = useTranslation()
  const [shown, setShown] = useState(open)
  return (
    <Card withBorder>
      <Stack>
        <Title order={4}>{t('verification.instruction.title')}</Title>
        <Text>{t('verification.instruction.noPasswords')}</Text>
        {!shown && (
          <Button variant="default" w="fit-content" onClick={() => setShown(true)}>
            {t('verification.instruction.show')}
          </Button>
        )}
        {shown && <Steps />}
      </Stack>
    </Card>
  )
}
