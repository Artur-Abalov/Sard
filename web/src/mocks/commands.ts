// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

// The command that adds a repository to the built-in agent, as the server builds it (F4b, Р23–Р25, Р29):
// the mock repeats the rules so the wizard meets the same answers, field by field.

const SELF_STORAGE_DIR = '/var/lib/sard-self'
const KEY_ID_PROMPT = 'read -r -p "S3 access key ID: " SARD_S3_KEY_ID'
const KEY_ID_FLAG = '--access-key-id "$SARD_S3_KEY_ID"'
const RESTART = 'docker compose restart self-agent'
const RUN =
  'docker compose run --rm --no-deps -u 0 --cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FOWNER ' +
  '--cap-add SETUID --cap-add SETGID self-agent repo add'
const CONFIG = '--no-restart --config /etc/sard/self/agent.yaml'
const MAX_PATH = 512
const MAX_PORT = 65535

const LABEL = '[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?'
const DNS_NAME = new RegExp(`^(?=.{1,253}$)${LABEL}(\\.${LABEL})*$`)
const IPV6 = /^\[[0-9A-Fa-f:.]+\]$/
const BUCKET = /^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$/
const REGION = /^[a-z0-9-]{1,64}$/
const USER = /^[a-z_][a-z0-9_-]{0,31}$/
const SEGMENT = /^[A-Za-z0-9._-]+$/
const FINGERPRINT = /^SHA256:[A-Za-z0-9+/]{43}$/
const PORT = /^[1-9][0-9]{0,4}$/
const ENDPOINT = /^https?:\/\/([^:/@?#\s]+|\[[0-9A-Fa-f:.]+\])(:[0-9]+)?$/
const PLAIN_WORD = /^[A-Za-z0-9._-]+$/

export type Params = Record<string, string | undefined>

export interface Built {
  address: string
  commands: string[]
  backend: 's3' | 'sftp' | 'local'
}

const validHost = (host: string) => DNS_NAME.test(host) || IPV6.test(host)
const validPort = (port: string) => PORT.test(port) && Number(port) <= MAX_PORT
const plainSegment = (s: string) => SEGMENT.test(s) && s !== '.' && s !== '..'
const validSegments = (path: string) =>
  path.length <= MAX_PATH && path.split('/').every(plainSegment)
const validAbsolute = (path: string) => path.startsWith('/') && validSegments(path.slice(1))

function validEndpoint(endpoint: string): boolean {
  const match = ENDPOINT.exec(endpoint)
  if (match === null) return false
  const port = (match[2] ?? '').slice(1)
  return validHost(match[1]) && (port === '' || validPort(port))
}

/** The first of self, self-2, self-3, ... that [taken] does not hold. */
export function freeName(taken: string[]): string {
  for (let n = 1; ; n++) {
    const name = n === 1 ? 'self' : `self-${n}`
    if (!taken.includes(name)) return name
  }
}

function shellWord(value: string): string {
  return PLAIN_WORD.test(value) ? value : `'${value.replaceAll("'", "'\\''")}'`
}

class Fields {
  readonly invalid: string[] = []
  private readonly values: Params

  constructor(values: Params) {
    this.values = values
  }

  required(name: string, valid: (value: string) => boolean): string | null {
    const value = this.values[name]
    if (value !== undefined && value !== '' && valid(value)) return value
    this.invalid.push(name)
    return null
  }

  optional(name: string, valid: (value: string) => boolean): string | null {
    const value = this.values[name]
    if (value === undefined || value === '') return null
    if (valid(value)) return value
    this.invalid.push(name)
    return null
  }
}

interface Plan {
  address: string
  flags: string[]
  asksKeyId: boolean
}

function s3(fields: Fields): Plan | null {
  const provider = fields.required('provider', (p) => ['aws', 'b2', 'custom'].includes(p))
  const custom = provider === 'custom'
  const region = custom ? null : fields.required('region', (r) => REGION.test(r))
  const endpoint = custom ? fields.required('endpoint', validEndpoint) : null
  const bucket = fields.required('bucket', (b) => BUCKET.test(b))
  const path = fields.optional('path', validSegments)
  if (fields.invalid.length > 0 || bucket === null) return null
  const location = bucket + (path === null ? '' : `/${path}`)
  if (endpoint !== null) {
    return { address: `s3:${endpoint}/${location}`, flags: [KEY_ID_FLAG], asksKeyId: true }
  }
  const flags = [`--provider ${provider}`, `--region ${region}`, KEY_ID_FLAG]
  return { address: `s3:${location}`, flags, asksKeyId: true }
}

function sftp(fields: Fields): Plan | null {
  const user = fields.required('user', (u) => USER.test(u))
  const host = fields.required('host', validHost)
  const port = fields.optional('port', validPort)
  const path = fields.required('path', validAbsolute)
  const fingerprint = fields.optional('hostKeyFingerprint', (f) => FINGERPRINT.test(f))
  if (fields.invalid.length > 0) return null
  const address =
    port === null
      ? `sftp:${user}@${host}:${path}`
      : `sftp://${user}@${host}:${Number(port)}/${path}`
  const flags = fingerprint === null ? [] : [`--host-key-fingerprint ${fingerprint}`]
  return { address, flags, asksKeyId: false }
}

function planOf(storage: string | null, name: string, fields: Fields): Plan | null {
  if (storage === 's3') return s3(fields)
  if (storage === 'sftp') return sftp(fields)
  if (storage === 'local')
    return { address: `${SELF_STORAGE_DIR}/${name}`, flags: [], asksKeyId: false }
  return null
}

/** The commands for [name] and [params], or the fields that do not fit (422 at each). */
export function buildCommand(name: string, params: Params): Built | { invalid: string[] } {
  const fields = new Fields(params)
  const storage = fields.required('storage', (s) => ['s3', 'sftp', 'local'].includes(s))
  const plan = planOf(storage, name, fields)
  if (plan === null || storage === null) return { invalid: fields.invalid }
  const words = [...plan.flags, shellWord(name), shellWord(plan.address)]
  const add = `${RUN} ${words.join(' ')} ${CONFIG}`
  const commands = [...(plan.asksKeyId ? [KEY_ID_PROMPT] : []), add, RESTART]
  return { address: plan.address, commands, backend: storage as Built['backend'] }
}

const MAX_LOCATION = 1024
const AUTHORITY = /^(?:s3:|sftp:)?(?:\/\/)?(?:https?:\/\/)?([^/]*)/

/** The address holds no password in user:password@, no control character and is not long (OQ-203). */
export function safeLocation(location: string): boolean {
  if (location === '' || location.length > MAX_LOCATION) return false
  if (/\p{Cc}/u.test(location)) return false
  const authority = AUTHORITY.exec(location)?.[1] ?? ''
  const at = authority.lastIndexOf('@')
  return !(at >= 0 && authority.slice(0, at).includes(':'))
}

/** The password-reveal command of a repository (Р27). */
export function passwordRevealCommand(repository: string): string {
  return (
    `docker compose exec self-agent sard-agent repo password ${shellWord(repository)} ` +
    '--reveal --config /etc/sard/self/agent.yaml'
  )
}
