// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import {
  AppShell,
  Group,
  SegmentedControl,
  Title,
  useMantineColorScheme,
  type MantineColorScheme,
} from '@mantine/core'
import { useTranslation } from 'react-i18next'
import { Route, Routes } from 'react-router'
import { languages } from './i18n'
import { Dashboard } from './pages/Dashboard'

const schemes: MantineColorScheme[] = ['light', 'dark', 'auto']

function Header() {
  const { t, i18n } = useTranslation()
  const { colorScheme, setColorScheme } = useMantineColorScheme()

  return (
    <Group h="100%" px="md" justify="space-between">
      <Title order={3}>{t('app.title')}</Title>
      <Group>
        <SegmentedControl
          aria-label={t('app.language')}
          value={i18n.language}
          onChange={(lng) => void i18n.changeLanguage(lng)}
          data={languages.map((lng) => ({ value: lng, label: lng.toUpperCase() }))}
        />
        <SegmentedControl
          aria-label={t('app.colorScheme')}
          value={colorScheme}
          onChange={(value) => setColorScheme(value as MantineColorScheme)}
          data={schemes.map((scheme) => ({ value: scheme, label: t(`app.${scheme}`) }))}
        />
      </Group>
    </Group>
  )
}

export function App() {
  return (
    <AppShell header={{ height: 60 }} padding="md">
      <AppShell.Header>
        <Header />
      </AppShell.Header>
      <AppShell.Main>
        <Routes>
          <Route path="/" element={<Dashboard />} />
        </Routes>
      </AppShell.Main>
    </AppShell>
  )
}
