// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from './api/schema'

type Schemas = components['schemas']

export type InstallStep = Schemas['InstallStep']

// What the install block asks the server for. Everything else - commands, addresses, the
// package file, step order - is the server's answer, shown as it comes.
export interface InstallChoice {
  arch: Schemas['InstallArch']
  format: Schemas['InstallFormat']
  fetch: Schemas['FetchTool']
}

export const INSTALL_DEFAULTS: InstallChoice = { arch: 'amd64', format: 'deb', fetch: 'curl' }

export const ARCHES: Schemas['InstallArch'][] = ['amd64', 'arm64']
export const FORMATS: Schemas['InstallFormat'][] = ['deb', 'rpm', 'tar']
export const FETCHES: Schemas['FetchTool'][] = ['curl', 'wget']

// Whether the note "the package replaces only the program" is shown: deb and rpm share the
// postinstall and preremove scripts, the archive replaces files by the commands of the step.
export function keepsConfiguration(format: Schemas['InstallFormat']): boolean {
  return format === 'deb' || format === 'rpm'
}

// Where the release key is published outside the server: the section of the repository's README.
export const RELEASE_KEY_DOC = 'https://github.com/Artur-Abalov/sard#verifying-releases'

// What the copy button of a step puts on the clipboard: its commands, one to a line.
export function stepText(step: InstallStep): string {
  return step.commands.join('\n')
}

// In the dialog of a created token the enroll step shows the enrollCommand of that token,
// which the server sent whole; nothing else is composed here.
export function withEnrollCommand(steps: InstallStep[], enrollCommand: string): InstallStep[] {
  return steps.map((step) =>
    step.kind === 'enroll' ? { ...step, commands: [enrollCommand] } : step,
  )
}
