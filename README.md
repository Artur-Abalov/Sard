# Sard · [Русская версия](README.ru.md)

Sard is a backup console for the mixed infrastructure of small and mid-sized
businesses, built for the integrators and MSPs who look after it; its aim is
backups you have proven you can restore.

> Status: **early development, pre-release.** Restore from the console and
> automatic restore verification are not there yet; see
> [Not yet](#not-yet). Not production-ready.

## What it does today

- Backs up files and directories ([docs/plugins/files.md](docs/plugins/files.md)).
- Backs up PostgreSQL: `pg_dump` of a database in custom format plus the
  cluster's roles and tablespaces
  ([docs/plugins/postgresql.md](docs/plugins/postgresql.md)).
- Stores backups with [restic](https://restic.net) in S3-compatible storage,
  over SFTP or in a local directory; restic does deduplication and encryption
  ([docs/operator/05a-storage.md](docs/operator/05a-storage.md)).
- Runs backups on a cron schedule in a chosen time zone, with one catch-up
  run after downtime, or on demand from the console
  ([docs/operations/scheduler.md](docs/operations/scheduler.md)).
- Reports runs and missed schedules to Telegram
  ([docs/operations/notifications.md](docs/operations/notifications.md)).
- Installs agents from the server: deb, rpm and tar.gz for Linux amd64 and
  arm64, signed releases ([docs/operator/05-agents.md](docs/operator/05-agents.md)).

### Not yet

Restore from the console (today restore is a manual `restic restore`, see
[docs/demo.md](docs/demo.md)), restore verification, self-backup of the
server, other sources (MySQL, network devices, 1C), more than one tenant,
`sardctl` commands beyond `version`.

## How it works

```text
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

An agent runs on each host and dials out to the server over gRPC with mutual
TLS, so hosts open no inbound ports. Backup data goes from the agent straight
to storage, never through the server. Repositories, storage keys and scripts
are defined on the agent host and referenced by name only: the server holds no
storage keys ([ADR 0008](docs/adr/0008-crypto-provider.md)).

- Source plugins implement a single interface: prepare → dump → stream → verify
  ([ADR 0027](docs/adr/0027-plugin-sdk.md)).
- Enterprise modules plug in as Spring Boot starters via `server/.../extension`;
  the open core runs without them.

## Quickstart: a server in 10 minutes

On a Linux host (amd64 or arm64) with Docker Engine and the Compose plugin,
`curl` and `openssl`. This runs sard-server and PostgreSQL from the published
image of release `SARD_TAG`
([releases](https://github.com/Artur-Abalov/Sard/releases)); change only
that line to install another release.

<!-- quickstart:begin -->
```bash
SARD_TAG=v0.0.1-rc.1
mkdir -p ~/sard && cd ~/sard
curl -fsSLO "https://github.com/Artur-Abalov/Sard/releases/download/$SARD_TAG/docker-compose.yml"
curl -fsSL -o .env "https://github.com/Artur-Abalov/Sard/releases/download/$SARD_TAG/sard.env.example"
sed -i -e "s/^SARD_DB_PASSWORD=.*/SARD_DB_PASSWORD=$(openssl rand -hex 24)/" \
       -e "s/^SARD_ADMIN_PASSWORD=.*/SARD_ADMIN_PASSWORD=$(openssl rand -hex 16)/" .env
chmod 600 .env
docker compose up -d --wait
curl -fsS http://localhost:8080/api/v1/status
```
<!-- quickstart:end -->

The last command prints the server's version. The administrator password is
`SARD_ADMIN_PASSWORD` in `~/sard/.env`. The REST API listens on
`127.0.0.1:8080` only, because it is plain HTTP; the agents' port 9090
(gRPC, mutual TLS) is open to the network. Before enrolling agents from other
hosts, put the name they dial into `SARD_PKI_SERVER_NAMES` in `.env` and run
`docker compose up -d` again: see the comments in `.env` and the operator
guide, [docs/operator/](docs/operator/README.md) (in Russian). The whole
stage 1 demo, from a clean VM to a restored file, is
[docs/demo.md](docs/demo.md). Every release also carries
an offline archive of its images (sard-server, sard-agent, PostgreSQL), for hosts
without registry access:

```bash
gunzip -c sard-<VERSION>-images-linux-<ARCH>.tar.gz | docker load
docker compose up -d --wait --pull never
```

Next to the server runs `self-agent`, the agent `sard-self` that enrolls by
itself; it does not back up the installation yet. It reads the database
through a read-only role and the CA directory read-only
([docs/operations/self-agent.md](docs/operations/self-agent.md), in Russian).
Until then, back up the server by hand:
[docs/operator/06-data-and-backup.md](docs/operator/06-data-and-backup.md).
The CA key lives in the `sard_sard-pki` volume and the database in
`sard_postgres-data`; both survive `docker compose down` and are removed by
`docker compose down -v` only.

### Verifying releases

Agent releases (tar.gz, deb, rpm for linux/amd64 and arm64) are signed with
the Sard release key, [minisign](https://jedisct1.github.io/minisign/)
(Ed25519), over `SHA256SUMS`. Take the key from this repository, not from the
server you downloaded the package from: `deploy/release/sard-release.pub`.

| | |
|---|---|
| Key ID | `DF5D5B6DB257DBFA` |
| Public key | `RWT621eybVtd38CL7B33xZrcc8ArYiPt3GlKXyJuk9ZQzDnoUV+kLi2d` |

```bash
minisign -Vm SHA256SUMS -p sard-release.pub && sha256sum -c --ignore-missing SHA256SUMS
```

Details and the release process: [docs/release.md](docs/release.md).

## Documentation

Most documentation is in Russian.

- [docs/demo.md](docs/demo.md): stage 1 demo, step by step: server, agent, backup, Telegram, restore
- [docs/operator/](docs/operator/README.md): running the server: requirements, install, configuration, TLS, backup, upgrade, troubleshooting
- [docs/plugins/](docs/plugins/): source plugins and their settings
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
[ADR 0004](docs/adr/0004-hybrid-license-and-cla.md).

## Reporting a vulnerability

Do not open a public issue. Report privately as described in
[SECURITY.md](SECURITY.md).

## Contributing

Start with [CONTRIBUTING.md](CONTRIBUTING.md): the CLA, the quality gate and
the language of commits and pull requests.

### Repository layout

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

### Requirements

JDK 25, Go 1.27, Node.js 24, Docker (for the server's Testcontainers tests and
`make up`), GNU make. You don't need to install buf, golangci-lint or the other
Go tools: `make tools` builds the pinned versions into `.bin/`.

Kotlin test class names contain Cyrillic, so Gradle needs a UTF-8 locale.
`make` sets `LC_ALL=C.UTF-8` for its own Gradle targets; when you call
`./gradlew` or `scripts/gate.sh` directly, `export LC_ALL=C.UTF-8` first (see
[CONTRIBUTING.md](CONTRIBUTING.md)).

`.claude/hooks/session-start.sh` runs at the start of every Claude Code on the
web session. It installs JDK 25 and Node.js 24, builds the pinned Go tools,
downloads Go and npm dependencies, and warms the Gradle cache, so tests,
linters and the quality gate work straight away. Outside the remote
environment it does nothing.

### Build and check

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

### Running from the checkout

```bash
make up                                   # PostgreSQL + sard-server built from this checkout (deploy/.env is created from .env.example)
curl -s localhost:8080/api/v1/status      # {"version":"…","lastVerifiedRestoreAt":null}
# the console itself is served by the server at http://localhost:8080 (it is in the image)
cd web && npm install && npm run dev      # development: console at http://localhost:5173, API proxied to :8080
make down
```

The administrator password is the `SARD_ADMIN_PASSWORD` variable in
`deploy/.env` (next to `SARD_AGENT_ENDPOINT`), at least 12 characters long; the
server refuses to start without it. On the first `make up`, if `deploy/.env`
doesn't exist yet, a random password of 24+ characters is written there; the
command prints the path to the file but never the password itself
([ADR 0021](docs/adr/0021-admin-password-login.md)). The console opens on the
login page; the sign-out button is in the header.

Behind a TLS-terminating reverse proxy (Cloudflare Tunnel, nginx, Caddy) set
`SARD_FORWARD_HEADERS=native` in `deploy/.env` and keep port 8080 on the
loopback; without it login and every mutating request answer 403
`origin_rejected` ([ADR 0046](docs/adr/0046-reverse-proxy-forward-headers.md),
[docs/operator/04-tls-and-names.md](docs/operator/04-tls-and-names.md)).

Agent and CLI:

```bash
./agent/bin/sard-agent --version
./agent/bin/sard-agent --config deploy/agent/agent.example.yaml
./cli/bin/sardctl version
```
