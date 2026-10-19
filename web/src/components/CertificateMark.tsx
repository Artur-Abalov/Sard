// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Alert, Badge, Group, Table, Text } from '@mantine/core'
import { Trans, useTranslation } from 'react-i18next'
import type { components } from '../api/schema'
import { EMPTY } from '../format'
import { certificateMarkTone } from '../theme'
import { Time } from './Time'

type Certificate = components['schemas']['AgentCertificate'] | null

// The state of the certificate as the server reports it; a mark only where the theme gives one.
function State({ state }: { state: components['schemas']['AgentCertificateState'] }) {
  const { t } = useTranslation()
  const label = t(`enum.AgentCertificateState.${state}`)
  const tone = certificateMarkTone(state)
  return tone === null ? <>{label}</> : <Badge color={tone}>{label}</Badge>
}

// The "Certificate" column of the list: the date and a mark; a dash without a certificate.
export function CertificateCell({ certificate }: { certificate: Certificate }) {
  if (certificate === null) {
    return <Text span>{EMPTY}</Text>
  }
  return (
    <Group gap="xs" wrap="nowrap">
      <Time value={certificate.notAfter} />
      {certificateMarkTone(certificate.state) !== null && <State state={certificate.state} />}
    </Group>
  )
}

// Rows of the facts table of the card.
export function CertificateRows({ certificate }: { certificate: Certificate }) {
  const { t } = useTranslation()
  if (certificate === null) {
    return null
  }
  return (
    <>
      <Table.Tr>
        <Table.Th>{t('agent.certificateNotAfter')}</Table.Th>
        <Table.Td>
          <Time value={certificate.notAfter} />
        </Table.Td>
      </Table.Tr>
      <Table.Tr>
        <Table.Th>{t('agent.certificateDaysLeft')}</Table.Th>
        <Table.Td>{certificate.daysLeft}</Table.Td>
      </Table.Tr>
      <Table.Tr>
        <Table.Th>{t('agent.certificateState')}</Table.Th>
        <Table.Td>
          <State state={certificate.state} />
        </Table.Td>
      </Table.Tr>
      {certificate.renewalIssuedAt !== null && (
        <Table.Tr>
          <Table.Td colSpan={2}>
            <Trans
              i18nKey="agent.certificateRenewalIssued"
              components={{ when: <Time value={certificate.renewalIssuedAt} /> }}
            />
          </Table.Td>
        </Table.Tr>
      )}
    </>
  )
}

const HINTS = {
  expiring: 'agent.certificateExpiringHint',
  expired: 'agent.certificateExpiredHint',
} as const

// What to do, under the facts; the colour is the theme's.
export function CertificateAlert({ certificate }: { certificate: Certificate }) {
  const { t } = useTranslation()
  const tone = certificate === null ? null : certificateMarkTone(certificate.state)
  if (certificate === null || tone === null) {
    return null
  }
  const state = certificate.state as keyof typeof HINTS
  return <Alert color={tone}>{t(HINTS[state])}</Alert>
}
