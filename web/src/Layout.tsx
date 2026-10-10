// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import {
  AppShell,
  Box,
  Burger,
  Button,
  Group,
  SegmentedControl,
  useMantineColorScheme,
  type MantineColorScheme,
} from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { Outlet, useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { signOut } from './auth/session'
import { Wordmark } from './components/Wordmark'
import { languages } from './languages'
import { NavLink } from './NavLink'
import { contentMaxWidth } from './theme'

// Ends the session and leaves for /login without a redirect (Р9з) on 204 (signed
// out) or 401 (already signed out elsewhere) — either way there is no session to
// come back to. Any other outcome, including a network failure (Р9е: the cookie is
// HttpOnly, so the console cannot clear it itself), shows an error and stays put.
function LogoutButton() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [failed, setFailed] = useState(false)

  async function onClick() {
    setFailed(false)
    try {
      const response = await signOut()
      if (response.status === 204 || response.status === 401) {
        await navigate({ to: '/login' })
      } else {
        setFailed(true)
      }
    } catch {
      setFailed(true)
    }
  }

  return (
    <>
      <Button variant="subtle" onClick={() => void onClick()}>
        {t('app.logout')}
      </Button>
      {failed && <span role="alert">{t('login.unavailable')}</span>}
    </>
  )
}

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
        <Wordmark />
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
        <LogoutButton />
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
      <NavLink to="/tokens" label={t('tokens.title')} onClick={onNavigate} />
      <NavLink to="/sources" label={t('sources.title')} onClick={onNavigate} />
      <NavLink to="/runs" label={t('runs.title')} onClick={onNavigate} />
      <NavLink to="/settings" label={t('settings.title')} onClick={onNavigate} />
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
        <Box maw={contentMaxWidth}>
          <Outlet />
        </Box>
      </AppShell.Main>
    </AppShell>
  )
}
