// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

import type { components } from '../../api/schema'
import { http } from '../http'
import { noSession, notFound, PROBLEM } from '../problems'
import { state } from '../state'

type Schemas = components['schemas']
type Step = Schemas['InstallStep']

// A mock release: what the server of a release build would answer, with canned commands.
// The real commands, addresses and rules are the server's (docs/specs/server/agent-install.feature).
const AGENT_VERSION = 'v1.4.0'
const RESTIC_VERSION = '0.19.1'
const BASE = 'http://sard.example.com:8080/downloads/agent'
const DOC = 'https://github.com/Artur-Abalov/sard/blob/main/docs/operations/agent-install.md'
const KEY: Schemas['ReleaseKey'] = {
  id: 'DF5D5B6DB257DBFA',
  publicKey: 'RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d',
}

const ARCHES = ['amd64', 'arm64']
const FORMATS = ['deb', 'rpm', 'tar']
const RPM_ARCH: Record<string, string> = { amd64: 'x86_64', arm64: 'aarch64' }
const FETCHES = ['curl', 'wget']

function file(arch: string, format: string): string {
  if (format === 'deb') return `sard-agent_1.4.0_${arch}.deb`
  if (format === 'rpm') return `sard-agent-${AGENT_VERSION}.${RPM_ARCH[arch]}.rpm`
  return `sard-agent_${AGENT_VERSION}_linux_${arch}.tar.gz`
}

// What puts the package on the host: dpkg for a deb, rpm -Uvh for an rpm (the archive has its own steps).
function installCommand(arch: string, format: string): string {
  return `sudo ${format === 'rpm' ? 'rpm -Uvh' : 'dpkg -i'} ${file(arch, format)}`
}

function step(kind: Step['kind'], commands: string[], optional = false): Step {
  return { kind, commands, optional }
}

function download(arch: string, format: string, fetch: string): Step {
  const get = fetch === 'curl' ? 'curl -fsSLO' : 'wget -nv'
  const files = [file(arch, format), 'SHA256SUMS', 'SHA256SUMS.minisig']
  return step(
    'download',
    files.map((name) => `${get} ${BASE}/${name}`),
  )
}

function verification(arch: string, format: string, fetch: string): Step[] {
  return [
    download(arch, format, fetch),
    step('checksum', [`grep '  ${file(arch, format)}$' SHA256SUMS | sha256sum -c -`]),
    step('signature', [`minisign -Vm SHA256SUMS -P ${KEY.publicKey}`], true),
  ]
}

function installSteps(arch: string, format: string, fetch: string): Step[] {
  const user = 'sudo -u sard-agent sard-agent'
  return [
    ...verification(arch, format, fetch),
    step('install', [installCommand(arch, format)]),
    step('configure', ['sudo cp -n /etc/sard/agent.example.yaml /etc/sard/agent.yaml']),
    step('enroll', [`${user} enroll --server sard.example.com:9090 --token <TOKEN>`]),
    step('repo-init', [`${user} repo init --generate-password main`]),
    step('start', ['sudo systemctl enable --now sard-agent.service']),
  ]
}

function upgradeSteps(arch: string, format: string, fetch: string): Step[] {
  return [...verification(arch, format, fetch), step('upgrade', [installCommand(arch, format)])]
}

function refused(field: string): Schemas['ValidationProblem'] {
  return {
    type: 'about:blank',
    title: 'Unprocessable Content',
    status: 422,
    detail: null,
    code: 'validation_failed',
    errors: [{ field, message: 'has an unacceptable value' }],
  }
}

// The first of the query parameters that holds a value outside [allowed], by name.
function unacceptable(
  query: { get(name: string): string | null },
  allowed: Record<string, string[]>,
): string | null {
  const bad = Object.entries(allowed).find(([name, values]) => {
    const value = query.get(name)
    return value !== null && !values.includes(value)
  })
  return bad === undefined ? null : bad[0]
}

// The answer for an agent of the architecture it reported (null: it never registered).
function upgradeOf(
  arch: string | null,
  format: Schemas['InstallFormat'],
  fetch: string,
): Schemas['AgentUpgrade'] {
  const available = arch !== null && ARCHES.includes(arch)
  return {
    downloadsEnabled: true,
    agentVersion: AGENT_VERSION,
    resticVersion: RESTIC_VERSION,
    arch,
    format,
    signed: true,
    releaseKey: KEY,
    manualInstallDoc: DOC,
    steps: available ? upgradeSteps(arch, format, fetch) : [],
    reason: available ? null : arch === null ? 'arch_unknown' : 'arch_unavailable',
    // What the server decides: deb and rpm keep /etc/sard, the archive does not promise it, no commands promise nothing.
    keepsConfiguration: available && format !== 'tar',
  }
}

export const installHandlers = [
  http.get('/api/v1/agent-install', ({ query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const bad = unacceptable(query, { arch: ARCHES, format: FORMATS, fetch: FETCHES })
    if (bad !== null) return response(422).json(refused(bad), PROBLEM)
    const arch = (query.get('arch') ?? 'amd64') as Schemas['InstallArch']
    const format = (query.get('format') ?? 'deb') as Schemas['InstallFormat']
    return response(200).json({
      downloadsEnabled: true,
      agentVersion: AGENT_VERSION,
      resticVersion: RESTIC_VERSION,
      arch,
      format,
      signed: true,
      releaseKey: KEY,
      manualInstallDoc: DOC,
      steps: installSteps(arch, format, query.get('fetch') ?? 'curl'),
    })
  }),

  http.get('/api/v1/agents/{agentId}/upgrade', ({ params, query, response }) => {
    if (!state.signedIn) return response(401).json(noSession, PROBLEM)
    const agent = state.agents.find((a) => a.id === params.agentId)
    if (agent === undefined) return response(404).json(notFound, PROBLEM)
    const bad = unacceptable(query, { format: FORMATS, fetch: FETCHES })
    if (bad !== null) return response(422).json(refused(bad), PROBLEM)
    const format = (query.get('format') ?? 'deb') as Schemas['InstallFormat']
    return response(200).json(upgradeOf(agent.arch, format, query.get('fetch') ?? 'curl'))
  }),
]
