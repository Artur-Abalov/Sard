# Sard

Sard is an open-core backup orchestrator. A single console manages backups of
heterogeneous infrastructure (PostgreSQL, MySQL, files, MikroTik/Eltex
configurations, later 1C and others) and **proves that you can restore from
them**. The headline metric on the dashboard is "last verified restore N hours
ago".

Sard does not invent its own storage format: storage, deduplication and
encryption are handled by [restic](https://restic.net). Sard owns sources,
workflows, schedules, restore verification, reports and multi-tenancy.

> Status: **stage 1, under active development**. Agent enrollment over mTLS,
> the files plugin, restic repository initialisation, run dispatch, the stage 1
> REST API, admin login and Telegram notifications are in place; the product is
> not production-ready yet.

## Architecture

```
             browser / sardctl
                    │ REST /api/v1
                    ▼
           ┌─────────────────┐       PostgreSQL
           │   sard-server   │──────── (Flyway)
           │ Kotlin, Spring  │
           └─────────────────┘
                    ▲ gRPC + mTLS, connection opened by the agent
                    │
           ┌─────────────────┐   restic    ┌──────────────────────┐
           │   sard-agent    │────────────▶│  S3 / SFTP / local   │
           │  Go, on the host│             └──────────────────────┘
           └─────────────────┘
```

- The agent dials out to the server; hosts never open inbound ports.
- Backup data flows from the agent straight to storage, bypassing the server.
- The server is a control plane, not a key holder: repositories, secrets and
  scripts are defined on the agent host and referenced by name only
  ([ADR 0008](docs/adr/0008-crypto-provider.md)).
- Source plugins implement a single interface: prepare → dump → stream → verify.
- Enterprise modules plug in as Spring Boot starters via `server/.../extension`;
  the open core runs without them.

| Directory | What it is |
|---|---|
| `server/` | `sard-server`: Kotlin, Spring Boot 4.1, PostgreSQL, Flyway, gRPC |
| `agent/` | `sard-agent`: Go, single binary; `agent/plugins/sdk` is the plugin SDK |
| `cli/` | `sardctl`: Go, single binary |
| `proto/` | server ↔ agent gRPC contracts (buf); `proto/gen/go` is the generated Go code, `proto/jvm` the JVM bindings |
| `web/` | SPA: React, TypeScript, Vite, Mantine |
| `deploy/` | docker-compose, server Dockerfile, agent systemd unit |
| `examples/workflows/` | sample YAML workflows |
| `tools/` | development tooling (CRAP, pinned Go tools) |
| `test/e2e/` | end-to-end tests against built images |
| `docs/` | ADRs, session log, dependencies, specifications, CLA |

## Requirements

JDK 25, Go 1.27, Node.js 24, Docker (for the server's Testcontainers tests and
`make up`), GNU make. You don't need to install buf, golangci-lint or the other
Go tools: `make tools` builds the pinned versions into `.bin/`.

Kotlin test class names contain Cyrillic, so Gradle needs a UTF-8 locale.
`make` sets `LC_ALL=C.UTF-8` for its own Gradle targets; when you call
`./gradlew` or `scripts/gate.sh` directly, `export LC_ALL=C.UTF-8` first (see
[CONTRIBUTING.md](CONTRIBUTING.md)).

### Claude Code on the web

`.claude/hooks/session-start.sh` runs at the start of every Claude Code on the
web session. It installs JDK 25 and Node.js 24, builds the pinned Go tools,
downloads Go and npm dependencies, and warms the Gradle cache, so tests,
linters and the quality gate work straight away. Outside the remote
environment it does nothing.

## Build and check

```bash
make proto    # generate Go code from proto/ (the result is committed)
make build    # agent/bin/sard-agent, cli/bin/sardctl, server/build/libs/sard-server.jar, web/dist/
make test     # all test suites
make lint     # SPDX headers, buf lint, gofmt/vet/golangci-lint, spotless/detekt, oxlint/prettier/tsc
make gate     # full quality gate: coverage ≥ 80%, CRAP ≤ 6, complexity ≤ 8, mutation testing
make gate M=server   # one module: proto gen sdk tools agent cli server web
make e2e      # build the images, then run the end-to-end tests (needs Docker)
```

Thresholds are defined once, in `scripts/gate.sh`. A pull request with a red
gate is not accepted.

## Running

```bash
make up                                   # PostgreSQL + sard-server (deploy/.env is created from .env.example)
curl -s localhost:8080/api/v1/status      # {"version":"…","lastVerifiedRestoreAt":null}
cd web && npm install && npm run dev      # console at http://localhost:5173, API proxied to :8080
make down
```

The administrator password is the `SARD_ADMIN_PASSWORD` variable in
`deploy/.env` (next to `SARD_AGENT_ENDPOINT`), at least 12 characters long; the
server refuses to start without it. On the first `make up`, if `deploy/.env`
doesn't exist yet, a random password of 24+ characters is written there; the
command prints the path to the file but never the password itself
([ADR 0021](docs/adr/0021-admin-password-login.md)). The console opens on the
login page; the sign-out button is in the header.

Stage 1 supports direct access to port 8080 only: behind a TLS-terminating
reverse proxy (or any proxy that rewrites `Host`) login and every mutating
request answer 403, and the session cookie doesn't get `Secure`
([ADR 0021](docs/adr/0021-admin-password-login.md)).

Agent and CLI:

```bash
./agent/bin/sard-agent --version
./agent/bin/sard-agent --config deploy/agent/agent.example.yaml
./cli/bin/sardctl version
```

## Verifying releases

Agent releases (tar.gz, deb, rpm for linux/amd64 and arm64) are signed with
the Sard release key, [minisign](https://jedisct1.github.io/minisign/)
(Ed25519), over `SHA256SUMS`. Take the key from this repository, not from the
server you downloaded the package from: `deploy/release/sard-release.pub`.

| | |
|---|---|
| Key ID | _set when the owner generates the key_ |
| Public key | _set when the owner generates the key_ |

```bash
minisign -Vm SHA256SUMS -p sard-release.pub && sha256sum -c --ignore-missing SHA256SUMS
```

Details and the release process: `docs/release.md`.

## Documentation

- [docs/adr/](docs/adr/README.md): architecture decision records
- [docs/specs/](docs/specs/): Gherkin specifications per module
- [docs/sessions/](docs/sessions/): work session log
- [docs/dependencies.md](docs/dependencies.md): third-party dependencies and their licenses
- [CLAUDE.md](CLAUDE.md): project constitution and definition of done

## License

Sard uses a hybrid license. The server, agent, CLI and web UI are
[AGPL-3.0-only](LICENSE): anyone who runs a modified version as a service must
publish their changes. The gRPC contracts (`proto/`, including the generated
code) and the plugin SDK (`agent/plugins/sdk`) are
[Apache-2.0](proto/LICENSE), so third-party plugins, including proprietary
ones, can be written without AGPL obligations. External contributions are
accepted after signing the [CLA](docs/legal/CLA.md); see
[CONTRIBUTING.md](CONTRIBUTING.md) and
[ADR 0004](docs/adr/0004-hybrid-license-and-cla.md).
