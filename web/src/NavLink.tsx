// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { NavLink as MantineNavLink, type NavLinkProps } from '@mantine/core'
import { createLink, type LinkComponent } from '@tanstack/react-router'
import type { Ref } from 'react'

type AnchorProps = Omit<NavLinkProps, 'href'> & { ref?: Ref<HTMLAnchorElement> }

function Anchor(props: AnchorProps) {
  return <MantineNavLink component="a" {...props} />
}

const Created = createLink(Anchor)

// Mantine NavLink driven by the router: the target is type-checked against the
// route tree and the item is highlighted while its route is active.
export const NavLink: LinkComponent<typeof Anchor> = (props) => (
  <Created activeProps={{ active: true }} {...props} />
)
