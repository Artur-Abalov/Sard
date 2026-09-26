# Sard — Project Constitution

These are not guidelines. This is the condition under which work counts as done.

Sard is an open-core backup orchestrator: one console manages backups of
heterogeneous infrastructure and proves they can be restored. Storage,
deduplication and encryption are restic's job; Sard owns sources,
workflows, schedules, restore verification, reports and multi-tenancy.

## Topology

```
proto/            gRPC contracts server <-> agent (buf), Apache-2.0
proto/gen/go/     generated Go code, committed, separate Go module, Apache-2.0
agent/            sard-agent, Go, single binary, AGPL-3.0
agent/plugins/sdk plugin SDK, separate Go module, Apache-2.0
cli/              sardctl, Go, AGPL-3.0
server/           sard-server, Kotlin + Spring Boot + PostgreSQL, AGPL-3.0
web/              React + TypeScript + Vite + Mantine SPA, AGPL-3.0
tools/            dev tooling (crap, pinned Go tools), outside go.work
deploy/ examples/ docs/ test/e2e/
```

## The constraints that shape everything else

1. **The agent dials out.** It is a gRPC client over mTLS; hosts never
   open inbound ports. The server sends commands over the stream the
   agent opened.
2. **Backup data never passes through the server.** The agent streams to
   storage (S3, SFTP, local disk) via restic.
3. **The open core runs without enterprise modules.** Enterprise features
   plug in through `server/.../extension` as Spring Boot starters; zero
   extensions is a supported, tested configuration.
4. **The license boundary is a code boundary.** `proto/` and
   `agent/plugins/sdk` are Apache-2.0 so third-party plugins avoid AGPL
   obligations. Nothing Apache-2.0 imports AGPL code.
5. **Seams are interfaces from day one:** `crypto.Provider` (key handover
   to restic) and `sdk.Plugin` (prepare → dump → stream → verify). The
   rest of the agent depends on the interfaces only.

## Definition of done, by module

Thresholds are defined once, in `scripts/gate.sh`.

| Module | Gate | Threshold | Tool |
|---|---|---|---|
| sdk, agent, cli, tools (Go) | tests | green | `go test` |
| | coverage | >= 80% statements | `go test -coverprofile` |
| | CRAP per function | <= 6 | `.bin/crap` |
| | complexity | <= 8 | `golangci-lint` (gocyclo) |
| | mutation score | >= 0.80 | `go-mutesting` |
| server (Kotlin) | tests, coverage, CRAP, complexity, mutation | as above | JaCoCo, `.bin/crap`, detekt, see ADR 0006 |
| web | lint, typecheck, unit tests of pure functions, build | green | ESLint, tsc, Vitest, Vite |
| proto | lint, generated code up to date | green | buf |

The web UI contains no business logic. If it shows up, move it to the
server — don't write a test for it in place.

## Rules that are not up for discussion

1. **Metrics are never eyeballed.** Never state a complexity, coverage or
   mutation value that a tool didn't produce.
2. **A failing gate is fixed at the root cause, not the symptom.**
   Disabling a test, weakening a threshold, `-x test`, `//nolint`,
   `t.Skip` — all forbidden (`scripts/claude/guard*.sh` enforce this).
3. **Test first, code second.**
4. **Specification before implementation** for product features: the
   `/ship-feature` pipeline (specifier → coder → cleaner → architect →
   hardener). Configuration, tooling and the initial skeleton are not run
   through it, but still pass `make gate`.
5. **Role boundaries are respected.** coder doesn't refactor unrelated
   code, cleaner doesn't change behavior, architect writes nothing.
6. **Every source file carries an SPDX header** (`AGPL-3.0-only`, or
   `Apache-2.0` under `proto/` and `agent/plugins/sdk/`) and
   `Copyright 2026 Artur Abalov`.
7. **Minimal dependencies.** Each third-party library must be needed now
   and is listed in `docs/dependencies.md` with its license. The agent
   uses only the Go stdlib, gRPC, protobuf and a YAML parser.
8. **Every non-trivial decision gets an ADR** in `docs/adr/`; every work
   session appends to `docs/sessions/`.

## Commands

```bash
make proto                    generate proto/gen/go (commit the result)
make build                    agent/bin/sard-agent, cli/bin/sardctl
make test                     all test suites
make lint                     license-check, buf lint, gofmt/vet/golangci-lint
make license-check            SPDX headers match the directory's license
make tools                    build pinned dev tools into .bin/
make gate                     full quality gate, all modules
make gate-fast                same without mutation testing
make gate M=tools             one module (proto gen sdk tools agent cli server web)
./scripts/gate.sh tools fast  same, direct
./scripts/crap.sh tools       CRAP table, worst first, never fails
```

## Go

- Dependencies go into constructors; tests use fakes, never the network
  or the file system of the host.
- `main` is a thin wrapper over a testable `run(args, stdout, stderr) int`.
- Generated code (`proto/gen/go`) is committed and never edited by hand.

## Kotlin

- `when` over a sealed type is exhaustive, no `else`.
- Coroutine dispatchers are injected, not pulled from `Dispatchers`.
- Generated `data class` methods, protobuf and serializers are not tested.
