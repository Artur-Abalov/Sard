// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import { Anchor, Button, type AnchorProps, type ButtonProps } from '@mantine/core'
import { createLink, type LinkComponent } from '@tanstack/react-router'
import type { Ref } from 'react'

type AnchorLinkProps = Omit<AnchorProps, 'href'> & { ref?: Ref<HTMLAnchorElement> }

function MantineAnchor(props: AnchorLinkProps) {
  return <Anchor component="a" {...props} />
}

type ButtonLinkProps = Omit<ButtonProps, 'href'> & { ref?: Ref<HTMLAnchorElement> }

function MantineButton(props: ButtonLinkProps) {
  return <Button component="a" {...props} />
}

const CreatedAnchor = createLink(MantineAnchor)
const CreatedButton = createLink(MantineButton)

// A text link driven by the router: the target is checked against the route tree.
export const AppLink: LinkComponent<typeof MantineAnchor> = (props) => <CreatedAnchor {...props} />

// A button that is a link.
export const ButtonLink: LinkComponent<typeof MantineButton> = (props) => (
  <CreatedButton {...props} />
)
