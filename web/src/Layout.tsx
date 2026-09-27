// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import {
  AppShell,
  Burger,
  Group,
  SegmentedControl,
  Title,
  useMantineColorScheme,
  type MantineColorScheme,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { Outlet } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'
import { languages } from './languages'
import { NavLink } from './NavLink'

const schemes: MantineColorScheme[] = ['light', 'dark', 'auto']

function Header({ opened, toggle }: { opened: boolean; toggle: () => void }) {
  const { t, i18n } = useTranslation()
  const { colorScheme, setColorScheme } = useMantineColorScheme()

  return (
    <Group h="100%" px="md" justify="space-between">
      <Group>
        <Burger
          opened={opened}
          onClick={toggle}
          hiddenFrom="sm"
          size="sm"
          aria-label={t('nav.toggle')}
        />
        <Title order={3}>{t('app.title')}</Title>
      </Group>
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

function Navigation({ onNavigate }: { onNavigate: () => void }) {
  const { t } = useTranslation()
  return (
    <>
      <NavLink
        to="/"
        label={t('dashboard.title')}
        onClick={onNavigate}
        activeOptions={{ exact: true }}
      />
      <NavLink to="/agents" label={t('agents.title')} onClick={onNavigate} />
      <NavLink to="/sources" label={t('sources.title')} onClick={onNavigate} />
      <NavLink to="/runs" label={t('runs.title')} onClick={onNavigate} />
    </>
  )
}

// The shell of every protected page: header, section navigation, the page itself.
export function Layout() {
  const [opened, { toggle, close }] = useDisclosure()
  return (
    <AppShell
      header={{ height: 60 }}
      navbar={{ width: 220, breakpoint: 'sm', collapsed: { mobile: !opened } }}
      padding="md"
    >
      <AppShell.Header>
        <Header opened={opened} toggle={toggle} />
      </AppShell.Header>
      <AppShell.Navbar p="xs">
        <Navigation onNavigate={close} />
      </AppShell.Navbar>
      <AppShell.Main>
        <Outlet />
      </AppShell.Main>
    </AppShell>
  )
}
