// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// The page being opened; a subset of the router's ParsedLocation.
export interface Destination {
  pathname: string
}

// Decides whether a protected page may open. It runs in the layout route's
// beforeLoad, before any loader, so a refused visitor never triggers data
// requests. Sign-in does not exist yet: every page opens. W1b replaces the body
// with a session check that throws redirect() to the sign-in page.
export function guard(_destination: Destination): void {}
