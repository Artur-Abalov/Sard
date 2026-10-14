// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Button, Card, Group, Stack, Table, Text, Title } from '@mantine/core'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { recoveryCardQuery } from '../api/queries'
import { ButtonLink } from '../components/links'
import { Loaded } from '../components/Loaded'
import { Mono } from '../components/Mono'
import {
  CARD_FIELDS,
  recoveryCardText,
  type Language,
  type RecoveryCard,
} from '../onboarding/recoveryCard'

function download(card: RecoveryCard, language: Language, fileName: string) {
  const blob = new Blob([recoveryCardText(card, language)], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = fileName
  link.click()
  URL.revokeObjectURL(url)
}

// The recovery card (Рк15, OQ-202): the fields the server reports, with the labels of the language, to print (the
// browser prints the page; the shell of the console is hidden, see printCss) and to download as text. It holds no
// password and no credentials: the server has none.
export function RecoveryCardPage({ back }: { back: '/setup' | '/settings' }) {
  const { t, i18n } = useTranslation()
  const query = useQuery(recoveryCardQuery())
  const language: Language = i18n.language === 'ru' ? 'ru' : 'en'
  return (
    <Stack align="center" py="md">
      <Card withBorder padding="lg" w="100%" maw={720}>
        <Stack>
          <Title order={2}>{t('recoveryCard.title')}</Title>
          <Text>{t('recoveryCard.intro')}</Text>
          <Loaded query={query}>
            {(card) => (
              <>
                <Table>
                  <Table.Tbody>
                    {CARD_FIELDS.map((field) => (
                      <Table.Tr key={field}>
                        <Table.Th w="35%">{t(`recoveryCard.${field}`)}</Table.Th>
                        <Table.Td>
                          {card[field] === null ? (
                            t(field === 'location' ? 'recoveryCard.locationUnknown' : 'common.dash')
                          ) : (
                            <Mono>{card[field]}</Mono>
                          )}
                        </Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
                <Group data-no-print>
                  <Button variant="default" onClick={() => window.print()}>
                    {t('recoveryCard.print')}
                  </Button>
                  <Button
                    variant="default"
                    onClick={() => download(card, language, t('recoveryCard.fileName'))}
                  >
                    {t('recoveryCard.download')}
                  </Button>
                </Group>
              </>
            )}
          </Loaded>
          <Group data-no-print>
            <ButtonLink to={back} variant="subtle">
              {t('recoveryCard.back')}
            </ButtonLink>
          </Group>
        </Stack>
      </Card>
    </Stack>
  )
}
